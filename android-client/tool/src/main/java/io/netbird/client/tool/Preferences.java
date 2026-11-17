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

    private final String keyPixiuSubscriptionSource = "pixiu_subscription_source";

    private final String keyPixiuSubscriptionSrvHost = "pixiu_subscription_srv_host";

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

    /**
     * Saves a URL the user typed. An empty value clears the slot so the next
     * SRV lookup can fill it. The same URL already stored from SRV stays SRV.
     */
    public void setPixiuSubscription(String url) {
        setUserSubscription(url);
    }

    /** Empty when the slot is empty. A legacy URL with no source counts as user. */
    public String getPixiuSubscriptionSource() {
        String url = getPixiuSubscription();
        String source = sharedPref.getString(keyPixiuSubscriptionSource, "");
        if ((source == null || source.isEmpty()) && url != null && !url.isEmpty()) {
            return "user";
        }
        return source == null ? "" : source;
    }

    public String getPixiuSubscriptionSrvHost() {
        String host = sharedPref.getString(keyPixiuSubscriptionSrvHost, "");
        return host == null ? "" : host;
    }

    public void setUserSubscription(String url) {
        String value = url == null ? "" : url.trim();
        if (value.isEmpty()) {
            sharedPref.edit()
                    .putString(keyPixiuSubscription, "")
                    .putString(keyPixiuSubscriptionSource, "")
                    .putString(keyPixiuSubscriptionSrvHost, "")
                    .apply();
            return;
        }
        String source = sharedPref.getString(keyPixiuSubscriptionSource, "");
        if ("srv".equals(source) && value.equals(getPixiuSubscription())) {
            return;
        }
        sharedPref.edit()
                .putString(keyPixiuSubscription, value)
                .putString(keyPixiuSubscriptionSource, "user")
                .putString(keyPixiuSubscriptionSrvHost, "")
                .apply();
    }

    public void setSrvSubscription(String url, String host) {
        sharedPref.edit()
                .putString(keyPixiuSubscription, url == null ? "" : url)
                .putString(keyPixiuSubscriptionSource, "srv")
                .putString(keyPixiuSubscriptionSrvHost, host == null ? "" : host)
                .apply();
    }

    public String getPixiuNode() {
        return sharedPref.getString(keyPixiuNode, "");
    }

    public void setPixiuNode(String name) {
        sharedPref.edit().putString(keyPixiuNode, name == null ? "" : name).apply();
    }
}
