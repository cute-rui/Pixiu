//! `_pixiu._tcp` lookup and the one Verge remote subscription it writes.
//!
//! DNS goes to system resolvers that are not loopback, link-local, or overlay.
//! The saved URL is always `https://<target>[:port]/pixiu.yaml` from the SRV
//! record. Redirect targets are fetched, then discarded.

use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr};
use std::sync::atomic::{AtomicU16, Ordering};
use std::time::{Duration, Instant};

use base64::{
    Engine as _,
    engine::general_purpose::{STANDARD, STANDARD_NO_PAD},
};
use network_interface::{NetworkInterface, NetworkInterfaceConfig as _};
use serde_yaml_ng::{Mapping, Value};
use tauri::Url;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpStream, UdpSocket};

use crate::config::profiles::{profiles_append_item_safe, profiles_patch_item_safe, profiles_save_file_safe};
use crate::config::{IVerge, PrfItem, PrfOption};
use crate::pixiu::daemon;

const MAX_BODY: usize = 8 << 20;
const SRV_QTYPE: u16 = 33;
const A_QTYPE: u16 = 1;
const AAAA_QTYPE: u16 = 28;
const CNAME_QTYPE: u16 = 5;
const DNS_CLASS_IN: u16 = 1;
static QUERY_ID: AtomicU16 = AtomicU16::new(1);

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ApplyReport {
    pub applied: bool,
    pub kind: String,
    pub message: String,
    pub url: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ManagementHost {
    Name(String),
    Ip,
    Invalid,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SrvRecord {
    pub priority: u16,
    pub weight: u16,
    pub port: u16,
    pub target: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ProfilePlan {
    Create { active: bool },
    Update { uid: String, file: String, active: bool },
    Blocked(usize),
    UidTaken,
}

pub async fn apply_server_profile() -> ApplyReport {
    let info = match daemon::snapshot().await {
        Ok(info) => info,
        Err(_) => {
            return report(false, "transient", "netbird daemon is not running", "");
        }
    };
    let host = match management_host(&info.management_url) {
        ManagementHost::Name(host) => host,
        ManagementHost::Ip => {
            return report(
                false,
                "missing",
                "The management address is an IP address, so _pixiu._tcp was not queried. The subscription was left unchanged.",
                "",
            );
        }
        ManagementHost::Invalid => {
            return report(
                false,
                "transient",
                "The management URL could not be read. The subscription was left unchanged.",
                "",
            );
        }
    };

    let servers = dns_servers(&info.interface_name).await;
    if servers.is_empty() {
        return report(
            false,
            "transient",
            format!("No DNS server outside the tunnel is available for {host}. The subscription was left unchanged."),
            "",
        );
    }

    let records = match query_srv(&host, &servers, &info.interface_name).await {
        Ok(records) => records,
        Err(err) => {
            return report(
                false,
                "transient",
                format!("Could not look up _pixiu._tcp on {host}: {err}. The subscription was left unchanged."),
                "",
            );
        }
    };
    let sorted = sort_srv(records);
    if sorted.is_empty() {
        return report(
            false,
            "missing",
            format!("No _pixiu._tcp record on {host}. The subscription was left unchanged."),
            "",
        );
    }

    let mut candidates = Vec::new();
    let mut only_unavailable = true;
    let mut saw_ip_target = false;
    for record in sorted {
        if record.port == 0 || record.target.is_empty() || record.target == "." {
            continue;
        }
        only_unavailable = false;
        match ascii_hostname(&record.target) {
            Some(target) => candidates.push(SrvRecord { target, ..record }),
            None => saw_ip_target = true,
        }
    }
    if candidates.is_empty() {
        if saw_ip_target {
            return rejected(&host);
        }
        if only_unavailable {
            return report(
                false,
                "missing",
                format!("No _pixiu._tcp record on {host}. The subscription was left unchanged."),
                "",
            );
        }
        return rejected(&host);
    }

    let mut saw_rejected = saw_ip_target;
    let mut saw_transient = false;
    let mut fetch_error = None;
    for record in candidates {
        let url = subscription_url(&record.target, record.port);
        let addrs = match resolve_host(&record.target, &servers, &info.interface_name).await {
            Ok(addrs) => addrs,
            Err(_) => {
                saw_transient = true;
                continue;
            }
        };
        if addresses_rejected(&addrs) {
            saw_rejected = true;
            continue;
        }
        if let Some(blocked) = blocked_remote_message(&url).await {
            return report(false, "unchanged", blocked, &url);
        }
        match fetch_profile(&url, &servers, &info.interface_name).await {
            Ok(body) => match normalize_subscription(&body) {
                Ok(yaml) => return write_profile(&host, &url, &yaml).await,
                Err(err) => {
                    return report(
                        false,
                        "unchanged",
                        format!(
                            "The profile at {url} is not a usable subscription: {err}. The subscription was left unchanged."
                        ),
                        &url,
                    );
                }
            },
            Err(err) => fetch_error = Some(err),
        }
    }

    if let Some(err) = fetch_error {
        return report(
            false,
            "unchanged",
            format!("{err} The subscription was left unchanged."),
            "",
        );
    }
    if saw_rejected && !saw_transient {
        return rejected(&host);
    }
    report(
        false,
        "transient",
        format!("Could not look up _pixiu._tcp on {host}. The subscription was left unchanged."),
        "",
    )
}

fn rejected(host: &str) -> ApplyReport {
    report(
        false,
        "rejected",
        format!(
            "The _pixiu._tcp target on {host} was rejected because it points at an overlay, loopback, link-local, or other local address. The subscription was left unchanged."
        ),
        "",
    )
}

fn report(applied: bool, kind: &str, message: impl Into<String>, url: &str) -> ApplyReport {
    ApplyReport {
        applied,
        kind: kind.to_string(),
        message: message.into(),
        url: url.to_string(),
    }
}

pub fn management_host(raw: &str) -> ManagementHost {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return ManagementHost::Invalid;
    }
    let with_scheme = if trimmed.contains("://") {
        trimmed.to_string()
    } else {
        format!("https://{trimmed}")
    };
    let Ok(url) = Url::parse(&with_scheme) else {
        return ManagementHost::Invalid;
    };
    if url.scheme() != "https" && url.scheme() != "http" {
        return ManagementHost::Invalid;
    }
    let Some(host) = url.host_str() else {
        return ManagementHost::Invalid;
    };
    let host = host
        .trim_end_matches('.')
        .trim_matches(|ch: char| ch == '[' || ch == ']')
        .to_ascii_lowercase();
    if host.is_empty() {
        return ManagementHost::Invalid;
    }
    if host.parse::<IpAddr>().is_ok() {
        return ManagementHost::Ip;
    }
    ManagementHost::Name(host)
}

fn ascii_hostname(target: &str) -> Option<String> {
    match management_host(&format!("https://{target}")) {
        ManagementHost::Name(name) => Some(name),
        ManagementHost::Ip | ManagementHost::Invalid => None,
    }
}

pub fn subscription_url(host: &str, port: u16) -> String {
    if port == 443 {
        format!("https://{host}/pixiu.yaml")
    } else {
        format!("https://{host}:{port}/pixiu.yaml")
    }
}

pub fn sort_srv(mut records: Vec<SrvRecord>) -> Vec<SrvRecord> {
    records.sort_by(|left, right| {
        left.priority
            .cmp(&right.priority)
            .then(right.weight.cmp(&left.weight))
            .then(left.target.cmp(&right.target))
            .then(left.port.cmp(&right.port))
    });
    records
}

pub fn addresses_rejected(addrs: &[IpAddr]) -> bool {
    addrs.is_empty() || addrs.iter().copied().any(rejected_ip)
}

pub fn rejected_ip(addr: IpAddr) -> bool {
    if crate::pixiu::is_overlay(addr) {
        return true;
    }
    match addr {
        IpAddr::V4(ip) => ip.is_loopback() || ip.is_unspecified() || ip.is_link_local() || ip.is_multicast(),
        IpAddr::V6(ip) => ip.is_loopback() || ip.is_unspecified() || ip.is_multicast() || ip.is_unicast_link_local(),
    }
}

pub fn plan_profile(remotes: &[(String, String)], current: Option<&str>, uid_taken: bool) -> ProfilePlan {
    match remotes {
        [_, _, ..] => ProfilePlan::Blocked(remotes.len()),
        [(uid, file)] => ProfilePlan::Update {
            uid: uid.clone(),
            file: file.clone(),
            active: current == Some(uid.as_str()),
        },
        [] if uid_taken => ProfilePlan::UidTaken,
        [] => ProfilePlan::Create {
            active: current.is_none(),
        },
    }
}

pub fn nameservers_from_resolv(text: &str) -> Vec<IpAddr> {
    text.lines()
        .filter_map(|line| {
            let rest = line.trim().strip_prefix("nameserver")?;
            let token = rest.split_whitespace().next()?;
            token.parse().ok()
        })
        .collect()
}

pub fn nameservers_from_resolvectl(text: &str, skip_iface: &str) -> Vec<IpAddr> {
    let mut out = Vec::new();
    for line in text.lines() {
        let Some((left, right)) = line.split_once(':') else {
            continue;
        };
        if let Some(name) = iface_in_parens(left)
            && (is_tunnel_iface(name) || name.eq_ignore_ascii_case(skip_iface))
        {
            continue;
        }
        push_ips(right, &mut out);
    }
    out
}

pub fn nameservers_from_scutil(text: &str, skip_iface: &str) -> Vec<IpAddr> {
    let mut out = Vec::new();
    let mut pending = Vec::new();
    let mut iface = String::new();
    let flush = |iface: &str, pending: &mut Vec<IpAddr>, out: &mut Vec<IpAddr>| {
        if iface.is_empty() || !(is_tunnel_iface(iface) || iface.eq_ignore_ascii_case(skip_iface)) {
            out.extend(pending.iter().copied());
        }
        pending.clear();
    };
    for line in text.lines() {
        let line = line.trim();
        if line.starts_with("resolver ") {
            flush(&iface, &mut pending, &mut out);
            iface.clear();
            continue;
        }
        if let Some(rest) = line.strip_prefix("nameserver[")
            && let Some((_, addr)) = rest.split_once(':')
            && let Ok(ip) = addr.trim().parse::<IpAddr>()
        {
            pending.push(ip);
        }
        if let Some(name) = line.strip_prefix("if_index").and_then(iface_in_parens) {
            iface = name.to_string();
        }
    }
    flush(&iface, &mut pending, &mut out);
    out.retain(|ip| !rejected_ip(*ip));
    out
}

pub fn nameservers_from_netsh(text: &str, skip_iface: &str) -> Vec<IpAddr> {
    let mut out = Vec::new();
    let mut skip = false;
    for line in text.lines() {
        let trimmed = line.trim();
        if let Some(name) = trimmed.strip_prefix("Configuration for interface ") {
            let name = name.trim().trim_matches('"');
            skip = is_tunnel_iface(name) || name.eq_ignore_ascii_case(skip_iface);
            continue;
        }
        if skip {
            continue;
        }
        push_ips(trimmed, &mut out);
    }
    out
}

fn iface_in_parens(text: &str) -> Option<&str> {
    let start = text.rfind('(')?;
    let end = text[start + 1..].find(')')? + start + 1;
    let name = &text[start + 1..end];
    if name.is_empty() { None } else { Some(name) }
}

fn push_ips(text: &str, out: &mut Vec<IpAddr>) {
    for token in text.split(|ch: char| ch.is_whitespace() || ch == ',') {
        if let Ok(ip) = token.trim_matches(|ch: char| ch == '"' || ch == ',').parse::<IpAddr>()
            && !rejected_ip(ip)
            && !out.contains(&ip)
        {
            out.push(ip);
        }
    }
}

fn is_tunnel_iface(name: &str) -> bool {
    let name = name.to_ascii_lowercase();
    const PREFIXES: &[&str] = &[
        "tun",
        "utun",
        "wg",
        "tailscale",
        "clash",
        "mihomo",
        "ppp",
        "zt",
        "wt",
        "nebula",
        "zerotier",
        "outline",
        "warp",
        "amnezia",
    ];
    PREFIXES.iter().any(|prefix| name.starts_with(prefix)) || name.contains("netbird")
}

pub fn normalize_subscription(raw: &[u8]) -> Result<String, String> {
    let body = decode_subscription(raw)?;
    let text = std::str::from_utf8(&body).map_err(|_| "invalid subscription".to_string())?;
    let mut doc: Value = serde_yaml_ng::from_str(text).map_err(|_| "invalid subscription".to_string())?;
    let Some(map) = doc.as_mapping_mut() else {
        return Err("invalid subscription".to_string());
    };
    map.remove(Value::from("proxy-providers"));
    map.remove(Value::from("rule-providers"));
    map.remove(Value::from("sub-rules"));
    if let Some(Value::Sequence(groups)) = map.get_mut(Value::from("proxy-groups")) {
        for group in groups.iter_mut() {
            if let Some(group_map) = group.as_mapping_mut() {
                group_map.remove(Value::from("use"));
            }
        }
    }

    let concrete = concrete_proxy_names(map);
    let group = select_group_with_nodes(map, &concrete);
    let group = if let Some(group) = group {
        group
    } else {
        let mut proxies = Vec::new();
        for name in &concrete {
            proxies.push(Value::from(name.clone()));
        }
        let mut added = Mapping::new();
        added.insert(Value::from("name"), Value::from("Pixiu"));
        added.insert(Value::from("type"), Value::from("select"));
        added.insert(Value::from("proxies"), Value::Sequence(proxies));
        if let Some(Value::Sequence(seq)) = map.get_mut(Value::from("proxy-groups")) {
            seq.push(Value::Mapping(added));
        } else {
            map.insert(
                Value::from("proxy-groups"),
                Value::Sequence(vec![Value::Mapping(added)]),
            );
        }
        "Pixiu".to_string()
    };

    let rules_bad = map.get(Value::from("rules")).is_some_and(rules_need_reset);
    let rules_empty = map
        .get(Value::from("rules"))
        .and_then(Value::as_sequence)
        .is_none_or(|seq| seq.is_empty());
    if rules_bad || rules_empty {
        map.insert(
            Value::from("rules"),
            Value::Sequence(vec![Value::from(format!("MATCH,{group}"))]),
        );
    }

    let mut tun = map
        .get(Value::from("tun"))
        .and_then(Value::as_mapping)
        .cloned()
        .unwrap_or_default();
    tun.insert(Value::from("enable"), Value::from(false));
    tun.insert(Value::from("auto-route"), Value::from(false));
    tun.insert(Value::from("stack"), Value::from("gvisor"));
    tun.insert(Value::from("auto-detect-interface"), Value::from(false));
    map.insert(Value::from("tun"), Value::Mapping(tun));
    map.insert(Value::from("geo-auto-update"), Value::from(false));
    if map
        .get(Value::from("mode"))
        .and_then(Value::as_str)
        .unwrap_or("")
        .is_empty()
    {
        map.insert(Value::from("mode"), Value::from("rule"));
    }

    serde_yaml_ng::to_string(&doc).map_err(|err| err.to_string())
}

fn concrete_proxy_names(map: &Mapping) -> Vec<String> {
    let Some(seq) = map.get(Value::from("proxies")).and_then(Value::as_sequence) else {
        return Vec::new();
    };
    seq.iter()
        .filter_map(|item| {
            let item = item.as_mapping()?;
            let typ = item
                .get(Value::from("type"))
                .and_then(Value::as_str)
                .unwrap_or("")
                .to_ascii_lowercase();
            if matches!(
                typ.as_str(),
                "" | "select" | "url-test" | "fallback" | "load-balance" | "relay"
            ) {
                return None;
            }
            let name = item.get(Value::from("name")).and_then(Value::as_str)?;
            if name.is_empty() { None } else { Some(name.to_string()) }
        })
        .collect()
}

fn select_group_with_nodes(map: &Mapping, concrete: &[String]) -> Option<String> {
    let seq = map.get(Value::from("proxy-groups"))?.as_sequence()?;
    for group in seq {
        let Some(group) = group.as_mapping() else { continue };
        let typ = group.get(Value::from("type")).and_then(Value::as_str).unwrap_or("");
        if !typ.eq_ignore_ascii_case("select") {
            continue;
        }
        let name = group.get(Value::from("name")).and_then(Value::as_str).unwrap_or("");
        if name.is_empty() {
            continue;
        }
        let Some(proxies) = group.get(Value::from("proxies")).and_then(Value::as_sequence) else {
            continue;
        };
        let has_node = proxies
            .iter()
            .filter_map(Value::as_str)
            .any(|proxy| concrete.iter().any(|name| name == proxy));
        if has_node {
            return Some(name.to_string());
        }
    }
    None
}

fn rules_need_reset(rules: &Value) -> bool {
    let Some(seq) = rules.as_sequence() else {
        return true;
    };
    seq.iter().any(|rule| match rule.as_str() {
        Some(text) => {
            let upper = text.trim().to_ascii_uppercase();
            upper.starts_with("GEOIP,") || upper.starts_with("GEOSITE,") || upper.starts_with("RULE-SET,")
        }
        None => true,
    })
}

fn decode_subscription(raw: &[u8]) -> Result<Vec<u8>, String> {
    let trimmed = trim_body(raw);
    if trimmed.is_empty() {
        return Err("invalid subscription".to_string());
    }
    if is_yaml_document(trimmed) {
        return Ok(trimmed.to_vec());
    }
    let compact: Vec<u8> = trimmed
        .iter()
        .copied()
        .filter(|byte| !byte.is_ascii_whitespace())
        .collect();
    let text = std::str::from_utf8(&compact).map_err(|_| "invalid subscription".to_string())?;
    let decoded = STANDARD
        .decode(text)
        .or_else(|_| STANDARD_NO_PAD.decode(text))
        .map_err(|_| "invalid subscription".to_string())?;
    if is_yaml_document(&decoded) {
        Ok(decoded)
    } else {
        Err("invalid subscription".to_string())
    }
}

fn trim_body(raw: &[u8]) -> &[u8] {
    let mut body = raw;
    if body.starts_with(&[0xef, 0xbb, 0xbf]) {
        body = &body[3..];
    }
    while body.first().is_some_and(u8::is_ascii_whitespace) {
        body = &body[1..];
    }
    while body.last().is_some_and(u8::is_ascii_whitespace) {
        body = &body[..body.len() - 1];
    }
    body
}

fn is_yaml_document(raw: &[u8]) -> bool {
    let Ok(text) = std::str::from_utf8(raw) else {
        return false;
    };
    let Ok(Value::Mapping(map)) = serde_yaml_ng::from_str::<Value>(text) else {
        return false;
    };
    map.contains_key(Value::from("proxies")) || map.contains_key(Value::from("proxy-groups"))
}

async fn blocked_remote_message(url: &str) -> Option<String> {
    let profiles = crate::config::Config::profiles().await;
    let snap = profiles.latest_arc();
    let count = snap
        .items
        .as_ref()
        .map(|items| {
            items
                .iter()
                .filter(|item| item.itype.as_deref() == Some("remote"))
                .count()
        })
        .unwrap_or(0);
    drop(snap);
    if count >= 2 {
        Some(format!(
            "This server publishes {url}. More than one remote profile is already saved, so nothing was changed."
        ))
    } else {
        None
    }
}

async fn write_profile(host: &str, url: &str, yaml: &str) -> ApplyReport {
    let profiles = crate::config::Config::profiles().await;
    let snap = profiles.latest_arc();
    let items = snap.items.clone().unwrap_or_default();
    let current = snap.current.clone();
    drop(snap);
    let remotes: Vec<(String, String)> = items
        .iter()
        .filter(|item| item.itype.as_deref() == Some("remote"))
        .map(|item| {
            let uid = item.uid.clone().unwrap_or_default().to_string();
            let file = item
                .file
                .clone()
                .unwrap_or_else(|| format!("{uid}.yaml").into())
                .to_string();
            (uid, file)
        })
        .collect();
    let uid_taken = items.iter().any(|item| item.uid.as_deref() == Some("pixiu-srv"));
    let plan = plan_profile(&remotes, current.as_deref(), uid_taken);
    let now = chrono::Local::now().timestamp() as usize;
    let (uid, active) = match plan {
        ProfilePlan::Blocked(count) => {
            return report(
                false,
                "unchanged",
                format!(
                    "This server publishes {url}. {count} remote profiles are already saved, so nothing was changed."
                ),
                url,
            );
        }
        ProfilePlan::UidTaken => {
            return report(
                false,
                "unchanged",
                "The profile id pixiu-srv is already used by another profile. The subscription was left unchanged.",
                url,
            );
        }
        ProfilePlan::Create { active } => {
            let mut item = PrfItem {
                uid: Some("pixiu-srv".into()),
                itype: Some("remote".into()),
                name: Some("Pixiu".into()),
                file: Some("pixiu-srv.yaml".into()),
                url: Some(url.into()),
                updated: Some(now),
                option: Some(PrfOption {
                    allow_auto_update: Some(false),
                    ..PrfOption::default()
                }),
                file_data: Some(yaml.into()),
                ..PrfItem::default()
            };
            if let Err(err) = profiles_append_item_safe(&mut item).await {
                return report(
                    false,
                    "unchanged",
                    format!("Could not save the server profile: {err:#}. The subscription was left unchanged."),
                    url,
                );
            }
            if let Err(err) = profiles_save_file_safe().await {
                return report(
                    false,
                    "unchanged",
                    format!("Could not write profiles.yaml: {err:#}. The subscription was left unchanged."),
                    url,
                );
            }
            ("pixiu-srv".to_string(), active)
        }
        ProfilePlan::Update { uid, file, active } => {
            let option = items
                .iter()
                .find(|item| item.uid.as_deref() == Some(uid.as_str()))
                .map(|item| {
                    let mut option = item.option.clone().unwrap_or_default();
                    option.allow_auto_update = Some(false);
                    option
                });
            let path = match crate::utils::dirs::app_profiles_dir() {
                Ok(dir) => dir.join(&file),
                Err(err) => {
                    return report(
                        false,
                        "unchanged",
                        format!("Could not find the profile directory: {err:#}. The subscription was left unchanged."),
                        url,
                    );
                }
            };
            if let Err(err) = tokio::fs::write(&path, yaml.as_bytes()).await {
                return report(
                    false,
                    "unchanged",
                    format!("Could not write the profile file: {err}. The subscription URL was left unchanged."),
                    url,
                );
            }
            let patch = PrfItem {
                url: Some(url.into()),
                updated: Some(now),
                option,
                ..PrfItem::default()
            };
            if let Err(err) = profiles_patch_item_safe(&uid, &patch).await {
                return report(
                    false,
                    "unchanged",
                    format!("Could not update the subscription URL: {err:#}. The subscription URL was left unchanged."),
                    url,
                );
            }
            (uid, active)
        }
    };

    let verge = IVerge {
        pixiu_subscription_source: Some("srv".into()),
        pixiu_subscription_srv_host: Some(host.into()),
        pixiu_subscription_uid: Some(uid.clone().into()),
        ..IVerge::default()
    };
    if let Err(err) = crate::feat::patch_verge(&verge, false).await {
        log::warn!("saved the SRV profile but did not write verge.yaml subscription keys: {err:#}");
    }
    if let Err(err) = crate::core::Timer::global().refresh().await {
        log::warn!("saved the SRV profile but did not refresh profile timers: {err:#}");
    }
    crate::core::notify::announce(crate::core::notify::Refresh::Profiles);
    let uid_string = smartstring::alias::String::from(uid.as_str());
    crate::core::handle::Handle::notify_profile_changed(&uid_string);

    let mut message = if active {
        format!("Saved {url} from _pixiu._tcp on {host} into the subscription.")
    } else {
        format!("Saved {url} from _pixiu._tcp on {host}. The current profile was not switched.")
    };
    if active {
        match crate::feat::enhance_profiles().await {
            Ok(outcome) if outcome.is_valid() => {}
            Ok(outcome) => message.push_str(&format!(" The running config was not reloaded: {outcome}.")),
            Err(err) => message.push_str(&format!(" The running config was not reloaded: {err:#}.")),
        }
    }
    report(true, "written", message, url)
}

async fn dns_servers(skip_iface: &str) -> Vec<SocketAddr> {
    let mut ips = platform_dns(skip_iface).await;
    ips.retain(|ip| !rejected_ip(*ip));
    ips.sort();
    ips.dedup();
    ips.into_iter().map(|ip| SocketAddr::new(ip, 53)).collect()
}

#[cfg(target_os = "linux")]
async fn platform_dns(skip_iface: &str) -> Vec<IpAddr> {
    let mut ips = read_resolv("/etc/resolv.conf").await;
    ips.retain(|ip| !rejected_ip(*ip));
    if ips.is_empty() {
        ips = read_resolv("/run/systemd/resolve/resolv.conf").await;
        ips.retain(|ip| !rejected_ip(*ip));
    }
    if ips.is_empty()
        && let Some(text) = command_output("resolvectl", &["dns"]).await
    {
        ips = nameservers_from_resolvectl(&text, skip_iface);
    }
    ips
}

#[cfg(target_os = "macos")]
async fn platform_dns(skip_iface: &str) -> Vec<IpAddr> {
    let mut ips = read_resolv("/etc/resolv.conf").await;
    ips.retain(|ip| !rejected_ip(*ip));
    if ips.is_empty()
        && let Some(text) = command_output("scutil", &["--dns"]).await
    {
        ips = nameservers_from_scutil(&text, skip_iface);
    }
    ips
}

#[cfg(target_os = "windows")]
async fn platform_dns(skip_iface: &str) -> Vec<IpAddr> {
    let mut ips = Vec::new();
    if let Some(text) = command_output("netsh", &["interface", "ipv4", "show", "dnsservers"]).await {
        ips.extend(nameservers_from_netsh(&text, skip_iface));
    }
    if let Some(text) = command_output("netsh", &["interface", "ipv6", "show", "dnsservers"]).await {
        ips.extend(nameservers_from_netsh(&text, skip_iface));
    }
    ips
}

#[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
async fn platform_dns(_skip_iface: &str) -> Vec<IpAddr> {
    Vec::new()
}

async fn read_resolv(path: &str) -> Vec<IpAddr> {
    let Ok(text) = tokio::fs::read_to_string(path).await else {
        return Vec::new();
    };
    nameservers_from_resolv(&text)
}

async fn command_output(program: &str, args: &[&str]) -> Option<String> {
    let fut = tokio::process::Command::new(program).args(args).output();
    let out = tokio::time::timeout(Duration::from_millis(1500), fut)
        .await
        .ok()?
        .ok()?;
    if !out.status.success() {
        return None;
    }
    String::from_utf8(out.stdout).ok()
}

async fn query_srv(host: &str, servers: &[SocketAddr], skip_iface: &str) -> Result<Vec<SrvRecord>, String> {
    let name = format!("_pixiu._tcp.{host}.");
    let message = query_name(&name, SRV_QTYPE, servers, skip_iface).await?;
    if message.rcode == 3 {
        return Ok(Vec::new());
    }
    if message.rcode != 0 {
        return Err(format!("DNS rcode {}", message.rcode));
    }
    Ok(message.srv)
}

async fn resolve_host(host: &str, servers: &[SocketAddr], skip_iface: &str) -> Result<Vec<IpAddr>, String> {
    let mut name = host.trim_end_matches('.').to_string();
    for _ in 0..3 {
        let (v4, v6) = tokio::join!(
            query_name(&name, A_QTYPE, servers, skip_iface),
            query_name(&name, AAAA_QTYPE, servers, skip_iface)
        );
        let mut addrs = Vec::new();
        let mut cname = None;
        let mut answered = false;
        for result in [v4, v6] {
            match result {
                Ok(message) if message.rcode == 0 || message.rcode == 3 => {
                    answered = true;
                    addrs.extend(message.addrs);
                    if cname.is_none() {
                        cname = message.cname;
                    }
                }
                Ok(_) | Err(_) => {}
            }
        }
        if !addrs.is_empty() {
            addrs.sort();
            addrs.dedup();
            return Ok(addrs);
        }
        if let Some(next) = cname {
            name = next;
            continue;
        }
        if answered {
            return Ok(Vec::new());
        }
        return Err("DNS lookup failed".to_string());
    }
    Ok(Vec::new())
}

struct DnsMessage {
    rcode: u16,
    srv: Vec<SrvRecord>,
    addrs: Vec<IpAddr>,
    cname: Option<String>,
}

async fn query_name(name: &str, qtype: u16, servers: &[SocketAddr], skip_iface: &str) -> Result<DnsMessage, String> {
    let packet = build_query(name, qtype)?;
    let (bind_v4, bind_v6) = physical_bind_addrs(skip_iface);
    let mut last = "DNS lookup failed".to_string();
    for server in servers {
        match query_server(*server, &packet, bind_v4, bind_v6).await {
            Ok(buf) => match parse_dns(&buf) {
                Ok(message) if message.rcode == 2 => last = "DNS SERVFAIL".to_string(),
                Ok(message) => return Ok(message),
                Err(err) => last = err,
            },
            Err(err) => last = err,
        }
    }
    Err(last)
}

async fn query_server(
    server: SocketAddr,
    packet: &[u8],
    bind_v4: Option<Ipv4Addr>,
    bind_v6: Option<Ipv6Addr>,
) -> Result<Vec<u8>, String> {
    let bound = query_udp(server, packet, Some((bind_v4, bind_v6))).await;
    let udp = match bound {
        Ok(buf) => buf,
        Err(err) if err == "DNS timeout" || err.contains("did not match") => return Err(err),
        Err(_) => query_udp(server, packet, None).await?,
    };
    if dns_truncated(&udp) {
        return query_tcp(server, packet).await;
    }
    Ok(udp)
}

async fn query_udp(
    server: SocketAddr,
    packet: &[u8],
    bind: Option<(Option<Ipv4Addr>, Option<Ipv6Addr>)>,
) -> Result<Vec<u8>, String> {
    let bind_addr = match (server, bind) {
        (SocketAddr::V4(_), Some((Some(ip), _))) => SocketAddr::from((ip, 0)),
        (SocketAddr::V6(_), Some((_, Some(ip)))) => SocketAddr::from((ip, 0)),
        (SocketAddr::V4(_), _) => SocketAddr::from((Ipv4Addr::UNSPECIFIED, 0)),
        (SocketAddr::V6(_), _) => SocketAddr::from((Ipv6Addr::UNSPECIFIED, 0)),
    };
    let socket = UdpSocket::bind(bind_addr).await.map_err(|err| err.to_string())?;
    let query_id = u16::from_be_bytes([packet[0], packet[1]]);
    let send = socket.send_to(packet, server);
    tokio::time::timeout(Duration::from_secs(3), send)
        .await
        .map_err(|_| "DNS timeout".to_string())?
        .map_err(|err| err.to_string())?;
    let mut buf = vec![0u8; 4096];
    let recv = socket.recv_from(&mut buf);
    let (len, _) = tokio::time::timeout(Duration::from_secs(3), recv)
        .await
        .map_err(|_| "DNS timeout".to_string())?
        .map_err(|err| err.to_string())?;
    buf.truncate(len);
    if buf.len() < 12 || u16::from_be_bytes([buf[0], buf[1]]) != query_id {
        return Err("DNS response did not match the query".to_string());
    }
    Ok(buf)
}

async fn query_tcp(server: SocketAddr, packet: &[u8]) -> Result<Vec<u8>, String> {
    let connect = TcpStream::connect(server);
    let mut stream = tokio::time::timeout(Duration::from_secs(3), connect)
        .await
        .map_err(|_| "DNS timeout".to_string())?
        .map_err(|err| err.to_string())?;
    let len = u16::try_from(packet.len()).map_err(|_| "DNS query is too long".to_string())?;
    stream
        .write_all(&len.to_be_bytes())
        .await
        .map_err(|err| err.to_string())?;
    stream.write_all(packet).await.map_err(|err| err.to_string())?;
    let mut size = [0u8; 2];
    tokio::time::timeout(Duration::from_secs(3), stream.read_exact(&mut size))
        .await
        .map_err(|_| "DNS timeout".to_string())?
        .map_err(|err| err.to_string())?;
    let size = u16::from_be_bytes(size) as usize;
    if size < 12 || size > 65535 {
        return Err("DNS TCP response has a bad length".to_string());
    }
    let mut buf = vec![0u8; size];
    tokio::time::timeout(Duration::from_secs(3), stream.read_exact(&mut buf))
        .await
        .map_err(|_| "DNS timeout".to_string())?
        .map_err(|err| err.to_string())?;
    Ok(buf)
}

fn physical_bind_addrs(skip_iface: &str) -> (Option<Ipv4Addr>, Option<Ipv6Addr>) {
    let Ok(interfaces) = NetworkInterface::show() else {
        return (None, None);
    };
    let mut v4 = None;
    let mut v6 = None;
    for iface in interfaces {
        if is_tunnel_iface(&iface.name) || iface.name.eq_ignore_ascii_case(skip_iface) {
            continue;
        }
        for addr in iface.addr {
            match addr.ip() {
                IpAddr::V4(ip) if v4.is_none() && !rejected_ip(IpAddr::V4(ip)) => v4 = Some(ip),
                IpAddr::V6(ip) if v6.is_none() && !rejected_ip(IpAddr::V6(ip)) => v6 = Some(ip),
                _ => {}
            }
        }
    }
    (v4, v6)
}

fn build_query(name: &str, qtype: u16) -> Result<Vec<u8>, String> {
    let id = QUERY_ID.fetch_add(1, Ordering::Relaxed);
    let mut packet = Vec::new();
    packet.extend_from_slice(&id.to_be_bytes());
    packet.extend_from_slice(&0x0100u16.to_be_bytes());
    packet.extend_from_slice(&1u16.to_be_bytes());
    packet.extend_from_slice(&[0, 0, 0, 0, 0, 0]);
    encode_name(name, &mut packet)?;
    packet.extend_from_slice(&qtype.to_be_bytes());
    packet.extend_from_slice(&DNS_CLASS_IN.to_be_bytes());
    Ok(packet)
}

fn encode_name(name: &str, out: &mut Vec<u8>) -> Result<(), String> {
    let name = name.trim_end_matches('.');
    if name.is_empty() {
        out.push(0);
        return Ok(());
    }
    for label in name.split('.') {
        if label.is_empty() || label.len() > 63 {
            return Err("DNS name has an empty or oversized label".to_string());
        }
        out.push(label.len() as u8);
        out.extend_from_slice(label.as_bytes());
    }
    out.push(0);
    Ok(())
}

fn dns_truncated(packet: &[u8]) -> bool {
    packet.len() >= 4 && u16::from_be_bytes([packet[2], packet[3]]) & 0x0200 != 0
}

fn parse_dns(packet: &[u8]) -> Result<DnsMessage, String> {
    if packet.len() < 12 {
        return Err("DNS response is too short".to_string());
    }
    let flags = u16::from_be_bytes([packet[2], packet[3]]);
    let qd = u16::from_be_bytes([packet[4], packet[5]]) as usize;
    let an = u16::from_be_bytes([packet[6], packet[7]]) as usize;
    let mut pos = 12;
    for _ in 0..qd {
        let (_, next) = read_name(packet, pos)?;
        pos = next + 4;
        if pos > packet.len() {
            return Err("DNS question is truncated".to_string());
        }
    }
    let mut message = DnsMessage {
        rcode: flags & 0x000f,
        srv: Vec::new(),
        addrs: Vec::new(),
        cname: None,
    };
    for _ in 0..an {
        let (_, next) = read_name(packet, pos)?;
        pos = next;
        if pos + 10 > packet.len() {
            return Err("DNS answer is truncated".to_string());
        }
        let qtype = u16::from_be_bytes([packet[pos], packet[pos + 1]]);
        let class = u16::from_be_bytes([packet[pos + 2], packet[pos + 3]]);
        let rdlen = u16::from_be_bytes([packet[pos + 8], packet[pos + 9]]) as usize;
        pos += 10;
        if pos + rdlen > packet.len() {
            return Err("DNS rdata is truncated".to_string());
        }
        let rdata = &packet[pos..pos + rdlen];
        if class == DNS_CLASS_IN {
            match qtype {
                SRV_QTYPE if rdlen >= 6 => {
                    let priority = u16::from_be_bytes([rdata[0], rdata[1]]);
                    let weight = u16::from_be_bytes([rdata[2], rdata[3]]);
                    let port = u16::from_be_bytes([rdata[4], rdata[5]]);
                    let (target, _) = read_name(packet, pos + 6)?;
                    message.srv.push(SrvRecord {
                        priority,
                        weight,
                        port,
                        target,
                    });
                }
                A_QTYPE if rdlen == 4 => {
                    message
                        .addrs
                        .push(IpAddr::V4(Ipv4Addr::new(rdata[0], rdata[1], rdata[2], rdata[3])));
                }
                AAAA_QTYPE if rdlen == 16 => {
                    let mut octets = [0u8; 16];
                    octets.copy_from_slice(rdata);
                    message.addrs.push(IpAddr::V6(Ipv6Addr::from(octets)));
                }
                CNAME_QTYPE => {
                    let (target, _) = read_name(packet, pos)?;
                    if message.cname.is_none() {
                        message.cname = Some(target);
                    }
                }
                _ => {}
            }
        }
        pos += rdlen;
    }
    Ok(message)
}

fn read_name(packet: &[u8], mut pos: usize) -> Result<(String, usize), String> {
    let mut labels = Vec::new();
    let mut end = None;
    for _ in 0..16 {
        let len = *packet.get(pos).ok_or_else(|| "DNS name is truncated".to_string())?;
        if len == 0 {
            if end.is_none() {
                end = Some(pos + 1);
            }
            return Ok((labels.join("."), end.unwrap_or(pos)));
        }
        if len & 0xc0 == 0xc0 {
            let second = *packet
                .get(pos + 1)
                .ok_or_else(|| "DNS name pointer is truncated".to_string())?;
            if end.is_none() {
                end = Some(pos + 2);
            }
            pos = (usize::from(len & 0x3f) << 8) | usize::from(second);
            continue;
        }
        if len & 0xc0 != 0 {
            return Err("DNS name has an unsupported label".to_string());
        }
        let start = pos + 1;
        let stop = start + usize::from(len);
        let label = packet
            .get(start..stop)
            .ok_or_else(|| "DNS name is truncated".to_string())?;
        let label = std::str::from_utf8(label).map_err(|_| "DNS name is not UTF-8".to_string())?;
        labels.push(label.to_string());
        pos = stop;
    }
    Err("DNS name has too many labels".to_string())
}

async fn fetch_profile(start: &str, servers: &[SocketAddr], skip_iface: &str) -> Result<Vec<u8>, String> {
    let deadline = Instant::now() + Duration::from_secs(20);
    let mut current = start.to_string();
    let mut redirects = 0u8;
    loop {
        let remain = deadline.saturating_duration_since(Instant::now());
        if remain.is_zero() {
            return Err(format!("Could not fetch {start}: timed out."));
        }
        let parsed =
            Url::parse(&current).map_err(|_| format!("Could not fetch {start}: the profile URL is invalid."))?;
        if parsed.scheme() != "https" {
            return Err(format!("Could not fetch {start}: a redirect left HTTPS."));
        }
        if !parsed.username().is_empty() || parsed.password().is_some() {
            return Err(format!("Could not fetch {start}: the profile URL contains user info."));
        }
        let Some(host) = parsed.host_str().map(str::to_string) else {
            return Err(format!("Could not fetch {start}: the profile URL has no host."));
        };
        if host.parse::<IpAddr>().is_ok() {
            return Err(format!("Could not fetch {start}: the profile host is an IP address."));
        }
        let addrs = resolve_host(&host, servers, skip_iface)
            .await
            .map_err(|err| format!("Could not fetch {start}: {err}."))?;
        if addresses_rejected(&addrs) {
            return Err(format!(
                "Could not fetch {start}: the profile host resolves to an overlay, loopback, link-local, or other local address."
            ));
        }
        let port = parsed.port_or_known_default().unwrap_or(443);
        let ip = addrs[0];
        let response = send_get(&parsed, &host, SocketAddr::new(ip, port), remain, skip_iface).await?;
        let status = response.status();
        if status.is_redirection() {
            if redirects >= 3 {
                return Err(format!("Could not fetch {start}: too many redirects."));
            }
            let location = response
                .headers()
                .get(reqwest::header::LOCATION)
                .and_then(|value| value.to_str().ok())
                .ok_or_else(|| format!("Could not fetch {start}: a redirect has no location."))?;
            current = parsed
                .join(location)
                .map_err(|_| format!("Could not fetch {start}: a redirect location is invalid."))?
                .to_string();
            redirects += 1;
            continue;
        }
        if !status.is_success() {
            return Err(format!("Could not fetch {start}: HTTP {status}."));
        }
        if response.content_length().is_some_and(|len| len > MAX_BODY as u64) {
            return Err(format!("Could not fetch {start}: the profile is larger than 8 MiB."));
        }
        let bytes = response
            .bytes()
            .await
            .map_err(|err| format!("Could not fetch {start}: {err}."))?;
        if bytes.len() > MAX_BODY {
            return Err(format!("Could not fetch {start}: the profile is larger than 8 MiB."));
        }
        return Ok(bytes.to_vec());
    }
}

async fn send_get(
    url: &Url,
    host: &str,
    addr: SocketAddr,
    timeout: Duration,
    skip_iface: &str,
) -> Result<reqwest::Response, String> {
    let mut builder = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .no_proxy()
        .timeout(timeout)
        .resolve(host, addr);
    if let Some(local) = local_addr_for(addr.ip(), skip_iface) {
        builder = builder.local_address(local);
    }
    let client = builder
        .build()
        .map_err(|err| format!("Could not fetch {url}: {err}."))?;
    client
        .get(url.clone())
        .header(reqwest::header::USER_AGENT, "pixiu")
        .send()
        .await
        .map_err(|err| format!("Could not fetch {url}: {err}."))
}

fn local_addr_for(destination: IpAddr, skip_iface: &str) -> Option<IpAddr> {
    let (v4, v6) = physical_bind_addrs(skip_iface);
    match destination {
        IpAddr::V4(_) => v4.map(IpAddr::V4),
        IpAddr::V6(_) => v6.map(IpAddr::V6),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn management_host_strips_port_and_rejects_literals() {
        assert_eq!(
            management_host("https://api.example.com:33073"),
            ManagementHost::Name("api.example.com".into())
        );
        assert_eq!(
            management_host("https://api.netbird.io:443"),
            ManagementHost::Name("api.netbird.io".into())
        );
        assert_eq!(
            management_host("https://API.Example.com/path"),
            ManagementHost::Name("api.example.com".into())
        );
        assert_eq!(
            management_host("https://user:secret@api.example.com"),
            ManagementHost::Name("api.example.com".into())
        );
        assert_eq!(management_host("https://100.64.0.1:443"), ManagementHost::Ip);
        assert_eq!(management_host("https://[fc00::1]/"), ManagementHost::Ip);
        assert_eq!(management_host("https://[fc00::1]:33073"), ManagementHost::Ip);
        assert_eq!(management_host(""), ManagementHost::Invalid);
        assert_eq!(management_host("://"), ManagementHost::Invalid);
        assert_eq!(
            management_host("https://münchen.example"),
            ManagementHost::Name("xn--mnchen-3ya.example".into())
        );
    }

    #[test]
    fn subscription_url_omits_443_and_keeps_other_ports() {
        assert_eq!(
            subscription_url("profile.example.com", 443),
            "https://profile.example.com/pixiu.yaml"
        );
        assert_eq!(
            subscription_url("profile.example.com", 8443),
            "https://profile.example.com:8443/pixiu.yaml"
        );
    }

    #[test]
    fn srv_order_is_priority_then_weight_then_name_then_port() {
        let ordered = sort_srv(vec![
            record(10, 50, 443, "a.example"),
            record(0, 0, 443, "b.example"),
            record(0, 5, 8443, "b.example"),
            record(0, 5, 443, "b.example"),
            record(0, 5, 443, "a.example"),
        ]);
        let keys: Vec<_> = ordered
            .iter()
            .map(|item| (item.priority, item.weight, item.target.as_str(), item.port))
            .collect();
        assert_eq!(
            keys,
            vec![
                (0, 5, "a.example", 443),
                (0, 5, "b.example", 443),
                (0, 5, "b.example", 8443),
                (0, 0, "b.example", 443),
                (10, 50, "a.example", 443),
            ]
        );
    }

    #[test]
    fn rejects_overlay_loopback_link_local_and_multicast_only() {
        assert!(rejected_ip("100.64.0.1".parse().unwrap()));
        assert!(rejected_ip("100.127.255.1".parse().unwrap()));
        assert!(rejected_ip("fc00::1".parse().unwrap()));
        assert!(rejected_ip("fd12::1".parse().unwrap()));
        assert!(rejected_ip("127.0.0.53".parse().unwrap()));
        assert!(rejected_ip("0.0.0.0".parse().unwrap()));
        assert!(rejected_ip("169.254.1.1".parse().unwrap()));
        assert!(rejected_ip("224.0.0.1".parse().unwrap()));
        assert!(rejected_ip("::1".parse().unwrap()));
        assert!(rejected_ip("fe80::1".parse().unwrap()));
        assert!(rejected_ip("ff02::1".parse().unwrap()));
        assert!(!rejected_ip("100.63.0.1".parse().unwrap()));
        assert!(!rejected_ip("100.128.0.1".parse().unwrap()));
        assert!(!rejected_ip("8.8.8.8".parse().unwrap()));
        assert!(!rejected_ip("2001:db8::1".parse().unwrap()));
        assert!(addresses_rejected(&[
            "8.8.8.8".parse().unwrap(),
            "100.64.0.2".parse().unwrap()
        ]));
        assert!(!addresses_rejected(&[
            "8.8.8.8".parse().unwrap(),
            "1.1.1.1".parse().unwrap()
        ]));
    }

    #[test]
    fn resolv_and_link_dns_skip_tunnel_and_local_addresses() {
        let resolv = "nameserver 127.0.0.53\nnameserver 100.64.0.1\nnameserver 8.8.8.8\nnameserver fe80::1\n# nameserver 9.9.9.9\nnameserver 1.1.1.1\n";
        let kept: Vec<_> = nameservers_from_resolv(resolv)
            .into_iter()
            .filter(|ip| !rejected_ip(*ip))
            .collect();
        assert_eq!(
            kept,
            vec!["8.8.8.8".parse::<IpAddr>().unwrap(), "1.1.1.1".parse().unwrap()]
        );

        let resolvectl = "Global: 9.9.9.9\nLink 2 (enp0s3): 8.8.8.8\nLink 3 (wt0): 1.0.0.1 100.64.0.53\n";
        assert_eq!(
            nameservers_from_resolvectl(resolvectl, "wt0"),
            vec!["9.9.9.9".parse::<IpAddr>().unwrap(), "8.8.8.8".parse().unwrap()]
        );

        let scutil = "resolver #1\n  nameserver[0] : 8.8.8.8\n  if_index : 12 (en0)\nresolver #2\n  nameserver[0] : 1.1.1.1\n  if_index : 15 (utun3)\n";
        assert_eq!(
            nameservers_from_scutil(scutil, "utun3"),
            vec!["8.8.8.8".parse::<IpAddr>().unwrap()]
        );

        let netsh = "Configuration for interface \"Ethernet\"\n    DNS servers configured through DHCP:  8.8.8.8\nConfiguration for interface \"wt0\"\n    Statically Configured DNS Servers:    1.2.3.4\n";
        assert_eq!(
            nameservers_from_netsh(netsh, "wt0"),
            vec!["8.8.8.8".parse::<IpAddr>().unwrap()]
        );
    }

    #[test]
    fn profile_plan_creates_updates_or_blocks() {
        assert_eq!(plan_profile(&[], None, false), ProfilePlan::Create { active: true });
        assert_eq!(
            plan_profile(&[], Some("local"), false),
            ProfilePlan::Create { active: false }
        );
        assert_eq!(plan_profile(&[], Some("local"), true), ProfilePlan::UidTaken);
        assert_eq!(
            plan_profile(&[("remote-1".into(), "remote-1.yaml".into())], Some("remote-1"), false),
            ProfilePlan::Update {
                uid: "remote-1".into(),
                file: "remote-1.yaml".into(),
                active: true
            }
        );
        assert_eq!(
            plan_profile(&[("remote-1".into(), "remote-1.yaml".into())], Some("local"), false),
            ProfilePlan::Update {
                uid: "remote-1".into(),
                file: "remote-1.yaml".into(),
                active: false
            }
        );
        assert_eq!(
            plan_profile(
                &[("a".into(), "a.yaml".into()), ("b".into(), "b.yaml".into())],
                None,
                false
            ),
            ProfilePlan::Blocked(2)
        );
    }

    #[test]
    fn normalize_strips_providers_and_replaces_geo_rules() {
        let raw = br#"
proxies:
  - name: exit
    type: ss
    server: 100.64.0.8
    port: 443
proxy-groups:
  - name: Proxy
    type: select
    proxies:
      - exit
    use:
      - remote-set
proxy-providers:
  remote-set:
    type: http
    url: https://example.invalid/providers
rule-providers:
  geos:
    type: http
    url: https://example.invalid/rules
sub-rules:
  extra: []
rules:
  - GEOIP,CN,DIRECT
  - RULE-SET,geos,DIRECT
  - MATCH,Proxy
"#;
        let yaml = normalize_subscription(raw).unwrap();
        let doc: Value = serde_yaml_ng::from_str(&yaml).unwrap();
        let map = doc.as_mapping().unwrap();
        assert!(map.get(Value::from("proxy-providers")).is_none());
        assert!(map.get(Value::from("rule-providers")).is_none());
        assert!(map.get(Value::from("sub-rules")).is_none());
        let rules = map.get(Value::from("rules")).unwrap().as_sequence().unwrap();
        assert_eq!(rules.len(), 1);
        assert_eq!(rules[0].as_str(), Some("MATCH,Proxy"));
        let groups = map.get(Value::from("proxy-groups")).unwrap().as_sequence().unwrap();
        assert!(groups[0].as_mapping().unwrap().get(Value::from("use")).is_none());
        let tun = map.get(Value::from("tun")).unwrap().as_mapping().unwrap();
        assert_eq!(tun.get(Value::from("enable")).and_then(Value::as_bool), Some(false));
        assert_eq!(tun.get(Value::from("auto-route")).and_then(Value::as_bool), Some(false));
        assert_eq!(
            map.get(Value::from("geo-auto-update")).and_then(Value::as_bool),
            Some(false)
        );
    }

    #[test]
    fn normalize_builds_pixiu_group_and_accepts_base64() {
        let raw = b"proxies:\n  - {name: exit, type: ss, server: 100.64.0.8, port: 443}\n";
        let encoded = STANDARD.encode(raw);
        let yaml = normalize_subscription(encoded.as_bytes()).unwrap();
        assert!(yaml.contains("Pixiu"));
        assert!(yaml.contains("MATCH,Pixiu"));
        let empty = normalize_subscription(b"proxies: []\nproxy-groups: []\n").unwrap();
        assert!(empty.contains("Pixiu"));
        assert!(normalize_subscription(b"just text").is_err());
    }

    #[test]
    fn parses_srv_answer_with_a_compression_pointer() {
        let mut packet = Vec::new();
        packet.extend_from_slice(&[0x00, 0x01, 0x81, 0x80, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00]);
        encode_name("_pixiu._tcp.api.example.com", &mut packet).unwrap();
        packet.extend_from_slice(&[0x00, 33, 0x00, 1]);
        packet.extend_from_slice(&[0xc0, 0x0c]);
        packet.extend_from_slice(&[0x00, 33, 0x00, 1]);
        packet.extend_from_slice(&[0x00, 0x00, 0x01, 0x2c]);
        let mut rdata = vec![0, 0, 0, 0, 0x01, 0xbb];
        encode_name("profile.example.com", &mut rdata).unwrap();
        packet.extend_from_slice(&(rdata.len() as u16).to_be_bytes());
        packet.extend_from_slice(&rdata);

        let message = parse_dns(&packet).unwrap();
        assert_eq!(message.rcode, 0);
        assert_eq!(message.srv.len(), 1);
        assert_eq!(message.srv[0].port, 443);
        assert_eq!(message.srv[0].priority, 0);
        assert_eq!(message.srv[0].target, "profile.example.com");
        assert!(!dns_truncated(&packet));
    }

    fn record(priority: u16, weight: u16, port: u16, target: &str) -> SrvRecord {
        SrvRecord {
            priority,
            weight,
            port,
            target: target.into(),
        }
    }
}
