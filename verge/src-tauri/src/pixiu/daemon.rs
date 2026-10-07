//! Short gRPC reads of the local NetBird daemon.
//!
//! The JSON socket is off unless the daemon was started with it, so this talks
//! to the default unix socket or the Windows named pipe.

use std::time::Duration;

use anyhow::{Result, bail};
use bytes::Bytes;
use http::Request;
use serde::Serialize;

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NetBirdDaemon {
    pub running: bool,
    pub status: String,
    pub admin_url: String,
    pub interface_name: String,
    pub message: String,
    pub endpoints: Vec<String>,
}

pub async fn snapshot() -> Result<NetBirdDaemon> {
    match tokio::time::timeout(Duration::from_millis(1500), read_snapshot()).await {
        Ok(result) => result,
        Err(_) => bail!("netbird daemon is not running"),
    }
}

pub async fn status() -> NetBirdDaemon {
    match snapshot().await {
        Ok(info) => info,
        Err(_) => NetBirdDaemon {
            running: false,
            status: String::new(),
            admin_url: String::new(),
            interface_name: String::new(),
            message: "netbird daemon is not running".to_string(),
            endpoints: Vec::new(),
        },
    }
}

async fn read_snapshot() -> Result<NetBirdDaemon> {
    let config = grpc("/daemon.DaemonService/GetConfig", &[]).await?;
    let status_body = grpc("/daemon.DaemonService/Status", &[0x08, 0x01]).await?;
    let admin_url = first_string(&config, 5).unwrap_or_default();
    let interface_name = first_string(&config, 6).unwrap_or_default();
    let status = first_string(&status_body, 1).unwrap_or_default();
    let endpoints = endpoints_from_status(&status_body);
    if status.is_empty() && admin_url.is_empty() && interface_name.is_empty() && endpoints.is_empty() {
        bail!("netbird daemon is not running");
    }
    Ok(NetBirdDaemon {
        running: true,
        status,
        admin_url,
        interface_name,
        message: String::new(),
        endpoints,
    })
}

async fn grpc(path: &str, payload: &[u8]) -> Result<Vec<u8>> {
    #[cfg(unix)]
    {
        let stream = tokio::net::UnixStream::connect(socket_path()?).await?;
        return grpc_io(stream, path, payload).await;
    }
    #[cfg(windows)]
    {
        let stream = open_pipe().await?;
        return grpc_io(stream, path, payload).await;
    }
    #[cfg(not(any(unix, windows)))]
    {
        let _ = (path, payload);
        bail!("netbird daemon is not running")
    }
}

async fn grpc_io<T>(io: T, path: &str, payload: &[u8]) -> Result<Vec<u8>>
where
    T: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send + 'static,
{
    let (mut client, connection) = h2::client::handshake(io).await?;
    tokio::spawn(async move {
        let _ = connection.await;
    });
    let request = Request::builder()
        .method("POST")
        .uri(path)
        .header("content-type", "application/grpc")
        .header("te", "trailers")
        .body(())?;
    let (response, mut send) = client.send_request(request, false)?;
    let mut frame = Vec::with_capacity(5 + payload.len());
    frame.push(0);
    frame.extend_from_slice(&(payload.len() as u32).to_be_bytes());
    frame.extend_from_slice(payload);
    send.send_data(Bytes::from(frame), true)?;
    let response = response.await?;
    let mut body = response.into_body();
    let mut data = Vec::new();
    while let Some(chunk) = body.data().await {
        let chunk = chunk?;
        let len = chunk.len();
        data.extend_from_slice(&chunk);
        let _ = body.flow_control().release_capacity(len);
    }
    Ok(grpc_payload(&data))
}

fn grpc_payload(buf: &[u8]) -> Vec<u8> {
    let mut out = Vec::new();
    let mut i = 0;
    while i + 5 <= buf.len() {
        let len = u32::from_be_bytes(buf[i + 1..i + 5].try_into().unwrap_or([0; 4])) as usize;
        i += 5;
        if i + len > buf.len() {
            break;
        }
        out.extend_from_slice(&buf[i..i + len]);
        i += len;
    }
    out
}

#[cfg(unix)]
fn socket_path() -> Result<std::path::PathBuf> {
    let primary = std::path::PathBuf::from("/var/run/netbird.sock");
    if primary.exists() {
        return Ok(primary);
    }
    if let Ok(entries) = std::fs::read_dir("/var/run/netbird") {
        for entry in entries.flatten() {
            let path = entry.path();
            if path.extension().and_then(|ext| ext.to_str()) == Some("sock") {
                return Ok(path);
            }
        }
    }
    bail!("netbird daemon is not running")
}

#[cfg(windows)]
async fn open_pipe() -> Result<tokio::net::windows::named_pipe::NamedPipeClient> {
    let paths = [
        r"\\.\pipe\ProtectedPrefix\Administrators\netbird",
        r"\\.\pipe\netbird",
    ];
    let mut last = None;
    for path in paths {
        match tokio::net::windows::named_pipe::ClientOptions::new().open(path) {
            Ok(client) => return Ok(client),
            Err(err) => last = Some(err),
        }
    }
    bail!("{}", last.map(|err| err.to_string()).unwrap_or_else(|| "netbird daemon is not running".into()))
}

pub fn endpoints_from_status(buf: &[u8]) -> Vec<String> {
    let mut out = Vec::new();
    for full in messages(buf, 2) {
        for peer in messages(&full, 4) {
            out.extend(strings(&peer, 10));
            out.extend(strings(&peer, 11));
            out.extend(strings(&peer, 18));
        }
        for relay in messages(&full, 5) {
            out.extend(strings(&relay, 1));
        }
    }
    out
}

fn first_string(buf: &[u8], field: u64) -> Option<String> {
    strings(buf, field).into_iter().next()
}

fn strings(buf: &[u8], field: u64) -> Vec<String> {
    messages(buf, field).into_iter().filter_map(|bytes| String::from_utf8(bytes).ok()).collect()
}

fn messages(buf: &[u8], field: u64) -> Vec<Vec<u8>> {
    let mut out = Vec::new();
    let mut i = 0;
    while i < buf.len() {
        let Some((key, next)) = varint(buf, i) else { break };
        i = next;
        let number = key >> 3;
        let wire = key & 7;
        match wire {
            0 => {
                let Some((_, next)) = varint(buf, i) else { break };
                i = next;
            }
            1 => i += 8,
            2 => {
                let Some((len, next)) = varint(buf, i) else { break };
                i = next;
                let end = i + len as usize;
                if end > buf.len() {
                    break;
                }
                if number == field {
                    out.push(buf[i..end].to_vec());
                }
                i = end;
            }
            5 => i += 4,
            _ => break,
        }
    }
    out
}

fn varint(buf: &[u8], mut i: usize) -> Option<(u64, usize)> {
    let mut value = 0u64;
    let mut shift = 0;
    while i < buf.len() && shift < 64 {
        let byte = buf[i];
        i += 1;
        value |= u64::from(byte & 0x7f) << shift;
        if byte & 0x80 == 0 {
            return Some((value, i));
        }
        shift += 7;
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn decodes_admin_url_and_peer_endpoint() {
        let peer = proto_string(10, "1.2.3.4:51820");
        let full = proto_bytes(4, &peer);
        let status = proto_bytes(2, &full);
        let config = proto_string(5, "https://admin.example");
        assert_eq!(first_string(&config, 5).as_deref(), Some("https://admin.example"));
        assert_eq!(endpoints_from_status(&status), vec!["1.2.3.4:51820".to_string()]);
    }

    fn proto_string(field: u64, value: &str) -> Vec<u8> {
        proto_bytes(field, value.as_bytes())
    }

    fn proto_bytes(field: u64, value: &[u8]) -> Vec<u8> {
        let mut out = Vec::new();
        write_varint(&mut out, (field << 3) | 2);
        write_varint(&mut out, value.len() as u64);
        out.extend_from_slice(value);
        out
    }

    fn write_varint(out: &mut Vec<u8>, mut value: u64) {
        loop {
            let mut byte = (value & 0x7f) as u8;
            value >>= 7;
            if value != 0 {
                byte |= 0x80;
            }
            out.push(byte);
            if value == 0 {
                break;
            }
        }
    }
}
