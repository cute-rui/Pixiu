//! Short gRPC reads of the local NetBird daemon.
//!
//! The JSON socket is off unless the daemon was started with it, so this talks
//! to the default unix socket or the Windows named pipe.

use std::time::Duration;

use anyhow::{Result, bail};
use bytes::Bytes;
use http::Request;
use serde::Serialize;

#[derive(Debug, Clone, Serialize, Default, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct NetBirdLink {
    pub url: String,
    pub connected: bool,
    pub error: String,
}

#[derive(Debug, Clone, Serialize, Default, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct NetBirdLocal {
    pub ip: String,
    pub ipv6: String,
    pub fqdn: String,
    pub pub_key: String,
    pub networks: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Default, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct NetBirdPeer {
    pub ip: String,
    pub ipv6: String,
    pub pub_key: String,
    pub conn_status: String,
    pub relayed: bool,
    pub fqdn: String,
    pub bytes_rx: i64,
    pub bytes_tx: i64,
    pub latency_ms: i64,
    pub relay_address: String,
    pub rosenpass_enabled: bool,
    pub networks: Vec<String>,
    pub local_endpoint: String,
    pub remote_endpoint: String,
}

#[derive(Debug, Clone, Serialize, Default, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct NetBirdNetwork {
    pub id: String,
    pub range: String,
    pub selected: bool,
    pub domains: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Default, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct NetBirdProfile {
    pub name: String,
    pub id: String,
    pub active: bool,
}

#[derive(Debug, Clone, Serialize, Default, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct NetBirdDaemon {
    pub running: bool,
    pub status: String,
    pub management_url: String,
    pub admin_url: String,
    pub interface_name: String,
    pub message: String,
    pub endpoints: Vec<String>,
    pub daemon_version: String,
    pub management: NetBirdLink,
    pub signal: NetBirdLink,
    pub local: NetBirdLocal,
    pub peers: Vec<NetBirdPeer>,
    pub networks: Vec<NetBirdNetwork>,
    pub profiles: Vec<NetBirdProfile>,
    pub disable_networks: bool,
    pub disable_profiles: bool,
}

pub async fn snapshot() -> Result<NetBirdDaemon> {
    match tokio::time::timeout(Duration::from_secs(4), read_snapshot()).await {
        Ok(result) => result,
        Err(_) => bail!("netbird daemon is not running"),
    }
}

pub async fn status() -> NetBirdDaemon {
    match snapshot().await {
        Ok(info) => info,
        Err(_) => NetBirdDaemon {
            running: false,
            message: "netbird daemon is not running".to_string(),
            ..NetBirdDaemon::default()
        },
    }
}

/// `Up` with `async` set, so the daemon returns before the tunnel is up.
pub async fn up() -> Result<()> {
    let payload = encode_bool(4, true);
    let _ = call("/daemon.DaemonService/Up", &payload).await?;
    Ok(())
}

pub async fn down() -> Result<()> {
    let _ = call("/daemon.DaemonService/Down", &[]).await?;
    Ok(())
}

pub async fn set_networks(ids: &[String], all: bool, select: bool) -> Result<()> {
    let mut payload = Vec::new();
    if all {
        payload.extend(encode_bool(3, true));
    } else {
        for id in ids {
            payload.extend(encode_string(1, id));
        }
    }
    let path = if select {
        "/daemon.DaemonService/SelectNetworks"
    } else {
        "/daemon.DaemonService/DeselectNetworks"
    };
    let _ = call(path, &payload).await?;
    Ok(())
}

pub async fn switch_profile(profile: &str) -> Result<()> {
    let payload = encode_string(1, profile);
    let _ = call("/daemon.DaemonService/SwitchProfile", &payload).await?;
    Ok(())
}

async fn read_snapshot() -> Result<NetBirdDaemon> {
    let config = grpc("/daemon.DaemonService/GetConfig", &[]).await?;
    let status_body = grpc("/daemon.DaemonService/Status", &[0x08, 0x01]).await?;
    let management_url = first_string(&config, 1).unwrap_or_default();
    let admin_url = first_string(&config, 5).unwrap_or_default();
    let interface_name = first_string(&config, 6).unwrap_or_default();
    let status = first_string(&status_body, 1).unwrap_or_default();
    let endpoints = endpoints_from_status(&status_body);
    if status.is_empty()
        && management_url.is_empty()
        && admin_url.is_empty()
        && interface_name.is_empty()
        && endpoints.is_empty()
    {
        bail!("netbird daemon is not running");
    }
    let full = messages(&status_body, 2).into_iter().next().unwrap_or_default();
    let features = call("/daemon.DaemonService/GetFeatures", &[]).await.unwrap_or_default();
    let networks = call("/daemon.DaemonService/ListNetworks", &[])
        .await
        .map(|body| networks_from_body(&body))
        .unwrap_or_default();
    let profiles = call("/daemon.DaemonService/ListProfiles", &[])
        .await
        .map(|body| profiles_from_body(&body))
        .unwrap_or_default();
    Ok(NetBirdDaemon {
        running: true,
        status,
        management_url,
        admin_url,
        interface_name,
        message: String::new(),
        endpoints,
        daemon_version: first_string(&status_body, 3).unwrap_or_default(),
        management: link_from_message(&messages(&full, 1).into_iter().next().unwrap_or_default()),
        signal: link_from_message(&messages(&full, 2).into_iter().next().unwrap_or_default()),
        local: local_from_message(&messages(&full, 3).into_iter().next().unwrap_or_default()),
        peers: peers_from_full(&full),
        networks,
        profiles,
        disable_networks: first_bool(&features, 3),
        disable_profiles: first_bool(&features, 1),
    })
}

async fn call(path: &str, payload: &[u8]) -> Result<Vec<u8>> {
    match tokio::time::timeout(Duration::from_secs(5), grpc(path, payload)).await {
        Ok(result) => result,
        Err(_) => bail!("netbird daemon is not running"),
    }
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
    let paths = [r"\\.\pipe\ProtectedPrefix\Administrators\netbird", r"\\.\pipe\netbird"];
    let mut last = None;
    for path in paths {
        match tokio::net::windows::named_pipe::ClientOptions::new().open(path) {
            Ok(client) => return Ok(client),
            Err(err) => last = Some(err),
        }
    }
    bail!(
        "{}",
        last.map(|err| err.to_string())
            .unwrap_or_else(|| "netbird daemon is not running".into())
    )
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
    messages(buf, field)
        .into_iter()
        .filter_map(|bytes| String::from_utf8(bytes).ok())
        .collect()
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

fn peers_from_full(full: &[u8]) -> Vec<NetBirdPeer> {
    messages(full, 4)
        .into_iter()
        .map(|peer| peer_from_message(&peer))
        .collect()
}

fn peer_from_message(buf: &[u8]) -> NetBirdPeer {
    NetBirdPeer {
        ip: first_string(buf, 1).unwrap_or_default(),
        ipv6: first_string(buf, 20).unwrap_or_default(),
        pub_key: first_string(buf, 2).unwrap_or_default(),
        conn_status: first_string(buf, 3).unwrap_or_default(),
        relayed: first_bool(buf, 5),
        fqdn: first_string(buf, 9).unwrap_or_default(),
        bytes_rx: first_varint(buf, 13).unwrap_or(0) as i64,
        bytes_tx: first_varint(buf, 14).unwrap_or(0) as i64,
        latency_ms: latency_ms(&messages(buf, 17).into_iter().next().unwrap_or_default()),
        relay_address: first_string(buf, 18).unwrap_or_default(),
        rosenpass_enabled: first_bool(buf, 15),
        networks: strings(buf, 16),
        local_endpoint: first_string(buf, 10).unwrap_or_default(),
        remote_endpoint: first_string(buf, 11).unwrap_or_default(),
    }
}

fn local_from_message(buf: &[u8]) -> NetBirdLocal {
    NetBirdLocal {
        ip: first_string(buf, 1).unwrap_or_default(),
        ipv6: first_string(buf, 8).unwrap_or_default(),
        fqdn: first_string(buf, 4).unwrap_or_default(),
        pub_key: first_string(buf, 2).unwrap_or_default(),
        networks: strings(buf, 7),
    }
}

fn link_from_message(buf: &[u8]) -> NetBirdLink {
    NetBirdLink {
        url: first_string(buf, 1).unwrap_or_default(),
        connected: first_bool(buf, 2),
        error: first_string(buf, 3).unwrap_or_default(),
    }
}

fn networks_from_body(buf: &[u8]) -> Vec<NetBirdNetwork> {
    messages(buf, 1)
        .into_iter()
        .map(|item| NetBirdNetwork {
            id: first_string(&item, 1).unwrap_or_default(),
            range: first_string(&item, 2).unwrap_or_default(),
            selected: first_bool(&item, 3),
            domains: strings(&item, 4),
        })
        .filter(|item| !item.id.is_empty())
        .collect()
}

fn profiles_from_body(buf: &[u8]) -> Vec<NetBirdProfile> {
    messages(buf, 1)
        .into_iter()
        .map(|item| NetBirdProfile {
            name: first_string(&item, 1).unwrap_or_default(),
            id: first_string(&item, 3).unwrap_or_default(),
            active: first_bool(&item, 2),
        })
        .filter(|item| !item.name.is_empty() || !item.id.is_empty())
        .collect()
}

fn latency_ms(buf: &[u8]) -> i64 {
    let seconds = first_varint(buf, 1).unwrap_or(0) as i64;
    let nanos = first_varint(buf, 2).unwrap_or(0) as i64;
    seconds.saturating_mul(1000).saturating_add(nanos / 1_000_000)
}

fn first_bool(buf: &[u8], field: u64) -> bool {
    first_varint(buf, field).unwrap_or(0) != 0
}

fn first_varint(buf: &[u8], field: u64) -> Option<u64> {
    varints(buf, field).into_iter().next()
}

fn varints(buf: &[u8], field: u64) -> Vec<u64> {
    let mut out = Vec::new();
    let mut i = 0;
    while i < buf.len() {
        let Some((key, next)) = varint(buf, i) else { break };
        i = next;
        let number = key >> 3;
        let wire = key & 7;
        match wire {
            0 => {
                let Some((value, next)) = varint(buf, i) else { break };
                i = next;
                if number == field {
                    out.push(value);
                }
            }
            1 => i += 8,
            2 => {
                let Some((len, next)) = varint(buf, i) else { break };
                i = next + len as usize;
                if i > buf.len() {
                    break;
                }
            }
            5 => i += 4,
            _ => break,
        }
    }
    out
}

fn encode_string(field: u64, value: &str) -> Vec<u8> {
    let mut out = encode_varint((field << 3) | 2);
    out.extend(encode_varint(value.len() as u64));
    out.extend_from_slice(value.as_bytes());
    out
}

fn encode_bool(field: u64, value: bool) -> Vec<u8> {
    let mut out = encode_varint(field << 3);
    out.push(u8::from(value));
    out
}

fn encode_varint(mut value: u64) -> Vec<u8> {
    let mut out = Vec::new();
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
        let management = proto_string(1, "https://api.example.com:33073");
        assert_eq!(first_string(&config, 5).as_deref(), Some("https://admin.example"));
        assert_eq!(
            first_string(&management, 1).as_deref(),
            Some("https://api.example.com:33073")
        );
        assert_eq!(endpoints_from_status(&status), vec!["1.2.3.4:51820".to_string()]);
    }

    #[test]
    fn decodes_peer_network_and_up_payload() {
        let mut peer = encode_string(1, "100.64.0.8");
        peer.extend(encode_string(3, "Connected"));
        peer.extend(encode_string(9, "host.example.net"));
        peer.extend(encode_bool(5, true));
        let full = proto_bytes(4, &peer);
        let status = proto_bytes(2, &full);
        let parsed = peers_from_full(&messages(&status, 2).into_iter().next().unwrap());
        assert_eq!(parsed[0].fqdn, "host.example.net");
        assert_eq!(parsed[0].ip, "100.64.0.8");
        assert!(parsed[0].relayed);
        assert_eq!(parsed[0].conn_status, "Connected");

        let mut network = encode_string(1, "exit");
        network.extend(encode_string(2, "0.0.0.0/0"));
        network.extend(encode_bool(3, true));
        let networks = networks_from_body(&proto_bytes(1, &network));
        assert_eq!(networks[0].id, "exit");
        assert!(networks[0].selected);
        assert_eq!(encode_bool(4, true), vec![0x20, 0x01]);
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
