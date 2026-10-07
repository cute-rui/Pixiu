package io.netbird.client.tool;

import android.content.Context;
import android.net.wifi.WifiInfo;
import android.net.wifi.WifiManager;

/**
 * SSIDs that must not auto-connect NetBird and Clash together.
 * One SSID per line in the netbird SharedPreferences key wifi_blacklist.
 */
public final class WifiBlacklist {

    private WifiBlacklist() {
    }

    public static String currentSsid(Context context) {
        try {
            WifiManager manager = (WifiManager) context.getApplicationContext()
                    .getSystemService(Context.WIFI_SERVICE);
            if (manager == null) {
                return null;
            }
            WifiInfo info = manager.getConnectionInfo();
            if (info == null) {
                return null;
            }
            String ssid = info.getSSID();
            if (ssid == null) {
                return null;
            }
            if (ssid.startsWith("\"") && ssid.endsWith("\"") && ssid.length() >= 2) {
                ssid = ssid.substring(1, ssid.length() - 1);
            }
            if (ssid.isEmpty() || "<unknown ssid>".equals(ssid)) {
                return null;
            }
            return ssid;
        } catch (Exception ignored) {
            return null;
        }
    }

    public static boolean listed(Context context) {
        String ssid = currentSsid(context);
        return ssid != null && new Preferences(context).isWifiBlacklisted(ssid);
    }

    public static String warning(String ssid) {
        return "warning: Wi-Fi \"" + ssid + "\" is on wifi_blacklist. Starting would connect both NetBird and Clash on that network.";
    }
}
