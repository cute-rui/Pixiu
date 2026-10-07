package io.netbird.client.tool;

import android.content.Context;
import android.content.SharedPreferences;

public class Preferences {

    public static final String ANONYMIZE_LEVEL_NONE = "none";

    public static final String ANONYMIZE_LEVEL_DEFAULT = "default";

    public static final String ANONYMIZE_LEVEL_STRICT = "strict";

    private final String keyTraceLog = "tracelog";

    private final String keyForceRelayConnection = "isConnectionForceRelayed";

    private final String keyAnonymizeLevel = "anonymizeLevel";

    private final String keyWifiBlacklist = "wifi_blacklist";

    private final String keyPixiuSubscription = "pixiu_subscription_url";

    private final String keyPixiuNode = "pixiu_selected_node";

    private final SharedPreferences sharedPref;

    public Preferences(Context context) {
       sharedPref = context.getSharedPreferences("netbird", Context.MODE_PRIVATE);
    }

    public boolean isTraceLogEnabled() {
       return sharedPref.getBoolean(keyTraceLog, false);
    }
    public void enableTraceLog() {
        sharedPref.edit().putBoolean(keyTraceLog, true).apply();
    }

    public void disableTraceLog() {
        sharedPref.edit().putBoolean(keyTraceLog, false).apply();
    }

    public String getAnonymizeLevel() {
        return sharedPref.getString(keyAnonymizeLevel, ANONYMIZE_LEVEL_DEFAULT);
    }

    public void setAnonymizeLevel(String level) {
        sharedPref.edit().putString(keyAnonymizeLevel, level).apply();
    }

    public boolean isConnectionForceRelayed() {
        return sharedPref.getBoolean(keyForceRelayConnection, true);
    }

    public void enableForcedRelayConnection() {
        sharedPref.edit().putBoolean(keyForceRelayConnection, true).apply();
    }

    public void disableForcedRelayConnection() {
        sharedPref.edit().putBoolean(keyForceRelayConnection, false).apply();
    }

    public static String defaultServer() {
        return "https://api.netbird.io";
    }

    /** One SSID per line. An empty list does not block. */
    public String getWifiBlacklist() {
        return sharedPref.getString(keyWifiBlacklist, "");
    }

    public void setWifiBlacklist(String value) {
        sharedPref.edit().putString(keyWifiBlacklist, value == null ? "" : value).apply();
    }

    public boolean isWifiBlacklisted(String ssid) {
        if (ssid == null || ssid.isEmpty()) {
            return false;
        }
        for (String line : getWifiBlacklist().split("\n")) {
            if (ssid.equals(line.trim()) && !line.trim().isEmpty()) {
                return true;
            }
        }
        return false;
    }

    public String getPixiuSubscription() {
        return sharedPref.getString(keyPixiuSubscription, "");
    }

    public void setPixiuSubscription(String url) {
        sharedPref.edit().putString(keyPixiuSubscription, url == null ? "" : url).apply();
    }

    public String getPixiuNode() {
        return sharedPref.getString(keyPixiuNode, "");
    }

    public void setPixiuNode(String name) {
        sharedPref.edit().putString(keyPixiuNode, name == null ? "" : name).apply();
    }
}
