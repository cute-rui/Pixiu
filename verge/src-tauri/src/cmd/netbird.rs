use super::CmdResult;
use crate::config::IVerge;
use crate::core::CoreManager;
use crate::feat;
use crate::pixiu::{self, NetBirdDaemon};
use serde::Serialize;

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PixiuConnect {
    pub started: bool,
    pub warning: String,
}

#[tauri::command]
pub async fn get_netbird_daemon() -> NetBirdDaemon {
    pixiu::daemon::status().await
}

#[tauri::command]
pub async fn get_pixiu_wifi_blacklist() -> CmdResult<String> {
    Ok(pixiu::current_blacklist().await.join("\n"))
}

#[tauri::command]
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
    feat::patch_verge(&patch, false).await.map_err(|err| err.to_string().into())
}

/// Explicit connect. A listed SSID warns and does not start unless `confirm` is true.
#[tauri::command]
pub async fn pixiu_connect(confirm: bool) -> CmdResult<PixiuConnect> {
    if let Some(ssid) = pixiu::listed_ssid().await {
        let warning = pixiu::wifi_warning(&ssid);
        if !confirm {
            return Ok(PixiuConnect { started: false, warning });
        }
        pixiu::allow_next_start();
        let started = CoreManager::global().start_core().await.is_ok();
        let _ = tokio::process::Command::new("netbird").arg("up").spawn();
        return Ok(PixiuConnect { started, warning });
    }
    let started = CoreManager::global().start_core().await.is_ok();
    let _ = tokio::process::Command::new("netbird").arg("up").spawn();
    Ok(PixiuConnect { started, warning: String::new() })
}
