//! Runtime-only NetBird merge and Wi-Fi blacklist for the desktop app.
//!
//! Peer public ICE and relay addresses become `IP-CIDR` `DIRECT` `no-resolve`
//! rules above `MATCH`, and `tun.route-exclude-address` entries. Overlay
//! addresses stay on the tunnel. Overlay proxy servers are pinned to the
//! NetBird interface.

pub(crate) mod daemon;

use std::net::IpAddr;
use std::sync::atomic::{AtomicBool, Ordering};

use serde_yaml_ng::{Mapping, Value};

pub use daemon::NetBirdDaemon;

static STARTUP: AtomicBool = AtomicBool::new(true);
static EXPLICIT_ALLOW: AtomicBool = AtomicBool::new(false);

/// App startup has finished, so later core starts are explicit.
pub fn startup_finished() {
    STARTUP.store(false, Ordering::Release);
}

/// The next core start was confirmed on the NetBird page.
pub fn allow_next_start() {
    EXPLICIT_ALLOW.store(true, Ordering::Release);
}

/// What `start_core` should do when the current SSID is on the blacklist.
pub enum StartGate {
    Allow,
    Skip,
    Warn(String),
}

pub async fn core_start_gate() -> StartGate {
    let Some(ssid) = blacklisted_ssid().await else {
        return StartGate::Allow;
    };
    if EXPLICIT_ALLOW.swap(false, Ordering::AcqRel) {
        return StartGate::Allow;
    }
    if STARTUP.load(Ordering::Acquire) {
        return StartGate::Skip;
    }
    StartGate::Warn(wifi_warning(&ssid))
}

pub fn wifi_warning(ssid: &str) -> String {
    format!(
        "warning: Wi-Fi \"{ssid}\" is on wifi_blacklist. Starting would connect both NetBird and Clash on that network."
    )
}

pub async fn listed_ssid() -> Option<String> {
    blacklisted_ssid().await
}

async fn blacklisted_ssid() -> Option<String> {
    let list = crate::config::Config::verge()
        .await
        .data_arc()
        .pixiu_wifi_blacklist
        .clone()
        .unwrap_or_default();
    if list.is_empty() {
        return None;
    }
    let ssid = scan_ssid().await?;
    if list.iter().any(|item| item == &ssid) {
        Some(ssid)
    } else {
        None
    }
}

pub async fn current_blacklist() -> Vec<String> {
    crate::config::Config::verge()
        .await
        .data_arc()
        .pixiu_wifi_blacklist
        .clone()
        .unwrap_or_default()
}

/// Merges a live daemon snapshot into the runtime config. A down daemon leaves
/// the document unchanged.
pub async fn merge_runtime(config: Mapping) -> Mapping {
    let Ok(info) = daemon::snapshot().await else {
        return config;
    };
    merge_mapping(config, &info.interface_name, &info.endpoints)
}

pub fn merge_mapping(mut config: Mapping, interface_name: &str, endpoints: &[String]) -> Mapping {
    let iface = if interface_name.is_empty() { "wt0" } else { interface_name };
    let addrs = public_addrs(endpoints);
    if !addrs.is_empty() {
        insert_rules(&mut config, &addrs);
        exclude_tun(&mut config, &addrs);
    }
    pin_overlay_proxies(&mut config, iface);
    config
}

fn public_addrs(endpoints: &[String]) -> Vec<IpAddr> {
    let mut out = Vec::new();
    for endpoint in endpoints {
        for addr in ips_in(endpoint) {
            if is_overlay(addr) || out.contains(&addr) {
                continue;
            }
            out.push(addr);
        }
    }
    out
}

fn ips_in(text: &str) -> Vec<IpAddr> {
    let cleaned = text.replace(['[', ']', ',', '"'], " ");
    cleaned
        .split(|c: char| c.is_whitespace() || c == '/')
        .filter_map(ip_from_token)
        .collect()
}

fn ip_from_token(mut token: &str) -> Option<IpAddr> {
    if let Some(idx) = token.find("://") {
        token = &token[idx + 3..];
    }
    if let Ok(addr) = token.parse() {
        return Some(addr);
    }
    let (host, port) = token.split_once(':')?;
    if port.is_empty() || !port.bytes().all(|b| b.is_ascii_digit()) || host.contains(':') {
        return None;
    }
    host.parse().ok()
}

fn is_overlay(addr: IpAddr) -> bool {
    match addr {
        IpAddr::V4(v4) => {
            let octets = v4.octets();
            octets[0] == 100 && octets[1] & 0xc0 == 0x40
        }
        IpAddr::V6(v6) => v6.octets()[0] & 0xfe == 0xfc,
    }
}

fn cidr(addr: IpAddr) -> String {
    match addr {
        IpAddr::V4(_) => format!("{addr}/32"),
        IpAddr::V6(_) => format!("{addr}/128"),
    }
}

fn direct_rule(addr: IpAddr) -> String {
    format!("IP-CIDR,{},DIRECT,no-resolve", cidr(addr))
}

fn insert_rules(config: &mut Mapping, addrs: &[IpAddr]) {
    let key = Value::from("rules");
    let mut rules: Vec<String> = config
        .get(&key)
        .and_then(Value::as_sequence)
        .map(|seq| seq.iter().filter_map(|item| item.as_str().map(str::to_string)).collect())
        .unwrap_or_default();
    let fresh: Vec<String> = addrs
        .iter()
        .map(|addr| direct_rule(*addr))
        .filter(|rule| !rules.iter().any(|existing| existing == rule))
        .collect();
    if fresh.is_empty() {
        return;
    }
    let at = rules.iter().position(|rule| rule.starts_with("MATCH")).unwrap_or(rules.len());
    for (offset, rule) in fresh.into_iter().enumerate() {
        rules.insert(at + offset, rule);
    }
    config.insert(key, Value::Sequence(rules.into_iter().map(Value::from).collect()));
}

fn exclude_tun(config: &mut Mapping, addrs: &[IpAddr]) {
    let tun_key = Value::from("tun");
    let mut tun = config
        .get(&tun_key)
        .and_then(Value::as_mapping)
        .cloned()
        .unwrap_or_default();
    let exclude_key = Value::from("route-exclude-address");
    let mut excludes: Vec<String> = tun
        .get(&exclude_key)
        .and_then(Value::as_sequence)
        .map(|seq| seq.iter().filter_map(|item| item.as_str().map(str::to_string)).collect())
        .unwrap_or_default();
    for addr in addrs {
        let item = cidr(*addr);
        if !excludes.iter().any(|existing| existing == &item) {
            excludes.push(item);
        }
    }
    tun.insert(exclude_key, Value::Sequence(excludes.into_iter().map(Value::from).collect()));
    config.insert(tun_key, Value::Mapping(tun));
}

fn pin_overlay_proxies(config: &mut Mapping, iface: &str) {
    let key = Value::from("proxies");
    let Some(Value::Sequence(proxies)) = config.get_mut(&key) else {
        return;
    };
    for proxy in proxies.iter_mut() {
        let Value::Mapping(map) = proxy else { continue };
        let Some(server) = map.get(Value::from("server")).and_then(Value::as_str) else {
            continue;
        };
        let Ok(addr) = server.parse::<IpAddr>() else { continue };
        if !is_overlay(addr) {
            continue;
        }
        map.insert(Value::from("interface-name"), Value::from(iface));
    }
}

pub fn ssid_from_nmcli(out: &str) -> Option<String> {
    for line in out.lines() {
        let (active, ssid) = line.trim().split_once(':')?;
        if active == "yes" && !ssid.is_empty() {
            return Some(ssid.to_string());
        }
    }
    None
}

pub fn ssid_from_airport(out: &str) -> Option<String> {
    for line in out.lines() {
        let line = line.trim();
        if let Some(name) = line.strip_prefix("Current Wi-Fi Network: ")
            && !name.is_empty()
        {
            return Some(name.to_string());
        }
    }
    None
}

pub fn ssid_from_netsh(out: &str) -> Option<String> {
    let mut ssid = None;
    let mut connected = false;
    for line in out.lines() {
        let Some((key, value)) = line.split_once(':') else { continue };
        let key = key.trim();
        let value = value.trim();
        if key.eq_ignore_ascii_case("State") && value.eq_ignore_ascii_case("connected") {
            connected = true;
        }
        if key.eq_ignore_ascii_case("SSID") && !value.is_empty() {
            ssid = Some(value.to_string());
        }
    }
    if connected { ssid } else { None }
}

async fn scan_ssid() -> Option<String> {
    #[cfg(target_os = "linux")]
    {
        if let Some(out) = command_output("nmcli", &["-t", "-f", "ACTIVE,SSID", "dev", "wifi"]).await {
            return ssid_from_nmcli(&out);
        }
        return command_output("iwgetid", &["-r"]).await.and_then(|out| {
            let ssid = out.trim().to_string();
            if ssid.is_empty() { None } else { Some(ssid) }
        });
    }
    #[cfg(target_os = "macos")]
    {
        let out = command_output("networksetup", &["-getairportnetwork", "en0"]).await?;
        return ssid_from_airport(&out);
    }
    #[cfg(target_os = "windows")]
    {
        let out = command_output("netsh", &["wlan", "show", "interfaces"]).await?;
        return ssid_from_netsh(&out);
    }
    #[cfg(not(any(target_os = "linux", target_os = "macos", target_os = "windows")))]
    {
        None
    }
}

async fn command_output(program: &str, args: &[&str]) -> Option<String> {
    let fut = tokio::process::Command::new(program).args(args).output();
    let out = tokio::time::timeout(std::time::Duration::from_millis(1500), fut).await.ok()?.ok()?;
    if !out.status.success() {
        return None;
    }
    String::from_utf8(out.stdout).ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample() -> Mapping {
        serde_yaml_ng::from_str(
            r#"
proxies:
  - name: overlay
    type: ss
    server: 100.64.0.8
    port: 443
  - name: public
    type: ss
    server: 1.1.1.1
    port: 443
rules:
  - GEOIP,CN,DIRECT
  - MATCH,Proxy
tun:
  enable: true
"#,
        )
        .unwrap()
    }

    #[test]
    fn merge_excludes_public_endpoints_and_pins_overlay() {
        let endpoints = vec![
            "8.8.8.8:51820".into(),
            "100.64.1.1:51820".into(),
            "fd12::1".into(),
            "rels://1.2.3.4:443".into(),
            "[2001:db8::1]:3478".into(),
        ];
        let merged = merge_mapping(sample(), "nb0", &endpoints);
        let rules = strings(merged.get(Value::from("rules")).unwrap());
        let joined = rules.join("\n");
        assert!(joined.contains("IP-CIDR,8.8.8.8/32,DIRECT,no-resolve"));
        assert!(joined.contains("IP-CIDR,1.2.3.4/32,DIRECT,no-resolve"));
        assert!(joined.contains("IP-CIDR,2001:db8::1/128,DIRECT,no-resolve"));
        assert!(!joined.contains("100.64"));
        assert!(!joined.contains("fd12"));
        let direct = rules.iter().position(|r| r.starts_with("IP-CIDR,8.8.8.8/")).unwrap();
        let match_at = rules.iter().position(|r| r.starts_with("MATCH")).unwrap();
        assert!(direct < match_at);
        let tun = merged.get(Value::from("tun")).unwrap().as_mapping().unwrap();
        let excluded = strings(tun.get(Value::from("route-exclude-address")).unwrap()).join("\n");
        assert!(excluded.contains("8.8.8.8/32"));
        assert!(!excluded.contains("100.64"));
        let proxies = merged.get(Value::from("proxies")).unwrap().as_sequence().unwrap();
        assert_eq!(
            proxies[0].as_mapping().unwrap().get(Value::from("interface-name")).and_then(Value::as_str),
            Some("nb0")
        );
        assert!(proxies[1].as_mapping().unwrap().get(Value::from("interface-name")).is_none());
    }

    #[test]
    fn empty_interface_defaults_to_wt0_and_appends_without_match() {
        let config: Mapping = serde_yaml_ng::from_str(
            "rules:\n  - DOMAIN,example,DIRECT\nproxies:\n  - {name: a, server: fd12::1}\n",
        )
        .unwrap();
        let merged = merge_mapping(config, "", &["9.9.9.9:1".into()]);
        let rules = strings(merged.get(Value::from("rules")).unwrap());
        assert_eq!(rules.last().map(String::as_str), Some("IP-CIDR,9.9.9.9/32,DIRECT,no-resolve"));
        let tun = merged.get(Value::from("tun")).unwrap().as_mapping().unwrap();
        assert!(tun.get(Value::from("enable")).is_none());
        let proxy = &merged.get(Value::from("proxies")).unwrap().as_sequence().unwrap()[0];
        assert_eq!(
            proxy.as_mapping().unwrap().get(Value::from("interface-name")).and_then(Value::as_str),
            Some("wt0")
        );
    }

    #[test]
    fn warning_matches_the_cli() {
        assert_eq!(
            wifi_warning("Cafe"),
            "warning: Wi-Fi \"Cafe\" is on wifi_blacklist. Starting would connect both NetBird and Clash on that network."
        );
    }

    #[test]
    fn parsers_ignore_inactive_and_ethernet() {
        assert_eq!(ssid_from_nmcli("no:Other\nyes:Cafe\n").as_deref(), Some("Cafe"));
        assert!(ssid_from_nmcli("no:Cafe\n").is_none());
        assert_eq!(ssid_from_airport("Current Wi-Fi Network: Cafe\n").as_deref(), Some("Cafe"));
        assert!(ssid_from_airport("You are not associated with an AirPort network.\n").is_none());
        assert_eq!(
            ssid_from_netsh("    State                  : connected\n    SSID                   : Cafe\n").as_deref(),
            Some("Cafe")
        );
        assert!(ssid_from_netsh("    State                  : disconnected\n    SSID                   : Cafe\n").is_none());
    }

    fn strings(value: &Value) -> Vec<String> {
        value
            .as_sequence()
            .unwrap()
            .iter()
            .map(|item| item.as_str().unwrap().to_string())
            .collect()
    }
}
