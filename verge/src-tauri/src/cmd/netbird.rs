use super::CmdResult;
use crate::config::IVerge;
use crate::core::CoreManager;
use crate::feat;
use crate::pixiu::{self, NetBirdDaemon};
use serde::Serialize;

#[derive(Debug, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct PixiuConnect {
    pub started: bool,
    pub warning: String,
}

#[tauri::command]
#[specta::specta]
pub async fn get_netbird_daemon() -> NetBirdDaemon {
    pixiu::daemon::status().await
}

#[tauri::command]
#[specta::specta]
pub async fn get_pixiu_wifi_blacklist() -> CmdResult<String> {
    Ok(pixiu::current_blacklist().await.join("\n"))
}

#[tauri::command]
#[specta::specta]
pub async fn set_pixiu_wifi_blacklist(text: String) -> CmdResult {
    let list = text
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty())
        .map(str::to_string)
        .collect();
    let patch = IVerge {
        pixiu_wifi_blacklist: Some(list),
        ..IVerge::default()
    };
    feat::patch_verge(&patch, false)
        .await
        .map_err(|err| err.to_string().into())
}

/// Explicit connect. A listed SSID warns and does not start unless `confirm` is true.
#[tauri::command]
#[specta::specta]
pub async fn pixiu_connect(confirm: bool) -> CmdResult<PixiuConnect> {
    if let Some(ssid) = pixiu::listed_ssid().await {
        let warning = pixiu::wifi_warning(&ssid);
        if !confirm {
            return Ok(PixiuConnect {
                started: false,
                warning,
            });
        }
        pixiu::allow_next_start();
        let started = bring_up().await;
        return Ok(PixiuConnect { started, warning });
    }
    let started = bring_up().await;
    Ok(PixiuConnect {
        started,
        warning: String::new(),
    })
}

async fn bring_up() -> bool {
    let started = CoreManager::global().start_core().await.is_ok();
    if pixiu::daemon::up().await.is_err() {
        let _ = tokio::process::Command::new("netbird").arg("up").spawn();
    }
    started
}

#[tauri::command]
#[specta::specta]
pub async fn pixiu_disconnect() -> CmdResult {
    pixiu::daemon::down().await.map_err(|err| err.to_string().into())
}

#[tauri::command]
#[specta::specta]
pub async fn pixiu_set_networks(ids: Vec<String>, selected: bool, all: bool) -> CmdResult {
    pixiu::daemon::set_networks(&ids, all, selected)
        .await
        .map_err(|err| err.to_string().into())
}

#[tauri::command]
#[specta::specta]
pub async fn pixiu_switch_profile(profile: String) -> CmdResult {
    pixiu::daemon::switch_profile(&profile)
        .await
        .map_err(|err| err.to_string().into())
}

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct PixiuSrvApply {
    pub applied: bool,
    pub kind: String,
    pub message: String,
    pub url: String,
    pub host: String,
    pub current: bool,
}

/// Look up `_pixiu._tcp` on the current management host and write that profile
/// into the one Verge remote subscription.
#[tauri::command]
#[specta::specta]
pub async fn pixiu_apply_srv_profile() -> CmdResult<PixiuSrvApply> {
    let report = crate::pixiu::srv::apply_server_profile().await;
    Ok(PixiuSrvApply {
        applied: report.applied,
        kind: report.kind,
        message: report.message,
        url: report.url,
        host: report.host,
        current: report.current,
    })
}
