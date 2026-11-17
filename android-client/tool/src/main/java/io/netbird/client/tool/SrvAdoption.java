package io.netbird.client.tool;

import org.json.JSONArray;
import org.json.JSONObject;

import java.net.IDN;
import java.net.URI;
import java.net.URISyntaxException;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

/**
 * Decides whether a _pixiu._tcp result may replace the one subscription slot.
 * An empty slot can be filled. A URL the user saved is left in place until
 * they ask to use the server profile.
 */
public final class SrvAdoption {

    public static final String SOURCE_USER = "user";
    public static final String SOURCE_SRV = "srv";

    public static final String KIND_URL = "url";
    public static final String KIND_MISSING = "missing";
    public static final String KIND_TRANSIENT = "transient";
    public static final String KIND_REJECTED = "rejected";

    public static final int AUTO = 0;
    public static final int EXPLICIT = 1;
    public static final int PANEL = 2;
    public static final int USE_SERVER = 3;

    private SrvAdoption() {
    }

    public static final class Lookup {
        public final String kind;
        public final String host;
        public final String url;
        public final List<String> urls;
        public final String message;

        public Lookup(String kind, String host, String url, List<String> urls, String message) {
            this.kind = kind == null ? "" : kind;
            this.host = host == null ? "" : host;
            this.url = url == null ? "" : url;
            this.urls = urls == null ? new ArrayList<String>() : urls;
            this.message = message == null ? "" : message;
        }

        public static Lookup parse(String json) {
            if (json == null || json.isEmpty()) {
                return new Lookup(KIND_TRANSIENT, "", "", null, "Could not look up the server profile. The subscription was left unchanged.");
            }
            try {
                JSONObject object = new JSONObject(json);
                List<String> urls = new ArrayList<>();
                JSONArray array = object.optJSONArray("urls");
                if (array != null) {
                    for (int i = 0; i < array.length(); i++) {
                        String candidate = array.optString(i);
                        if (!candidate.isEmpty()) {
                            urls.add(candidate);
                        }
                    }
                }
                String url = object.optString("url");
                if (url.isEmpty() && !urls.isEmpty()) {
                    url = urls.get(0);
                }
                return new Lookup(object.optString("kind"), object.optString("host"), url, urls, object.optString("message"));
            } catch (Exception e) {
                return new Lookup(KIND_TRANSIENT, "", "", null, "Could not look up the server profile. The subscription was left unchanged.");
            }
        }

        public static Lookup failure(String message) {
            String text = message == null || message.isEmpty()
                    ? "Could not look up the server profile. The subscription was left unchanged."
                    : message;
            return new Lookup(KIND_TRANSIENT, "", "", null, text);
        }
    }

    public static final class Decision {
        public final List<String> fetchUrls;
        public final boolean writeSrv;
        public final boolean showOffer;
        public final String offerUrl;
        public final String message;

        public Decision(List<String> fetchUrls, boolean writeSrv, boolean showOffer, String offerUrl, String message) {
            this.fetchUrls = fetchUrls == null ? new ArrayList<String>() : fetchUrls;
            this.writeSrv = writeSrv;
            this.showOffer = showOffer;
            this.offerUrl = offerUrl == null ? "" : offerUrl;
            this.message = message == null ? "" : message;
        }
    }

    /** Blank source with a saved URL is a subscription the user already set. */
    public static String effectiveSource(String url, String source) {
        if (url == null || url.isEmpty()) {
            return "";
        }
        if (source == null || source.isEmpty()) {
            return SOURCE_USER;
        }
        return source;
    }

    /**
     * Hostname used for _pixiu._tcp. Returns "" when the URL cannot be read,
     * and "ip" when the host is an address literal.
     */
    public static String managementHost(String raw) {
        if (raw == null) {
            return "";
        }
        String trimmed = raw.trim();
        if (trimmed.isEmpty()) {
            return "";
        }
        if (!trimmed.contains("://")) {
            trimmed = "https://" + trimmed;
        }
        String scheme = schemeOf(trimmed);
        if (!"https".equals(scheme) && !"http".equals(scheme)) {
            return "";
        }
        String host = hostOf(trimmed);
        if (host.isEmpty()) {
            return "";
        }
        host = stripDot(host).toLowerCase(Locale.ROOT);
        if (looksLikeIp(host)) {
            return "ip";
        }
        try {
            host = IDN.toASCII(host, IDN.USE_STD3_ASCII_RULES).toLowerCase(Locale.ROOT);
        } catch (IllegalArgumentException e) {
            return "";
        }
        if (host.isEmpty() || looksLikeIp(host)) {
            return host.isEmpty() ? "" : "ip";
        }
        return host;
    }

    public static Decision decide(String savedUrl, String source, boolean nodesLoaded, Lookup lookup, int trigger) {
        if (trigger == PANEL) {
            trigger = AUTO;
        }
        String saved = savedUrl == null ? "" : savedUrl;
        String effective = effectiveSource(saved, source);
        Lookup found = lookup == null ? Lookup.failure("") : lookup;
        if (trigger == USE_SERVER) {
            if (!KIND_URL.equals(found.kind) || found.url.isEmpty()) {
                return new Decision(new ArrayList<String>(), false, false, "", found.message);
            }
            return new Decision(candidates(found), true, false, "", "");
        }
        if (!KIND_URL.equals(found.kind)) {
            List<String> refresh = new ArrayList<>();
            if (trigger == EXPLICIT && !saved.isEmpty()) {
                refresh.add(saved);
            }
            return new Decision(refresh, false, false, "", found.message);
        }
        boolean same = found.url.equals(saved);
        if (effective.isEmpty()) {
            return new Decision(candidates(found), true, false, "", "");
        }
        if (SOURCE_USER.equals(effective)) {
            List<String> refresh = new ArrayList<>();
            if (trigger == EXPLICIT) {
                refresh.add(saved);
            }
            if (same) {
                return new Decision(refresh, false, false, "", "");
            }
            return new Decision(refresh, false, true, found.url, "");
        }
        if (same) {
            List<String> refresh = new ArrayList<>();
            if (trigger == EXPLICIT || !nodesLoaded) {
                refresh.add(saved);
            }
            return new Decision(refresh, false, false, "", "");
        }
        return new Decision(candidates(found), true, false, "", "");
    }

    private static List<String> candidates(Lookup lookup) {
        List<String> out = new ArrayList<>();
        if (lookup.urls != null) {
            for (String url : lookup.urls) {
                if (url != null && !url.isEmpty()) {
                    out.add(url);
                }
            }
        }
        if (out.isEmpty() && !lookup.url.isEmpty()) {
            out.add(lookup.url);
        }
        return out;
    }

    private static String schemeOf(String url) {
        int end = url.indexOf("://");
        if (end <= 0) {
            return "";
        }
        return url.substring(0, end).toLowerCase(Locale.ROOT);
    }

    private static String hostOf(String url) {
        try {
            URI uri = new URI(url);
            if (uri.getHost() != null && !uri.getHost().isEmpty()) {
                return stripBrackets(uri.getHost());
            }
        } catch (URISyntaxException ignored) {
            // Unicode hosts fail the ASCII URI parser; the slice below still works.
        }
        int start = url.indexOf("://");
        if (start < 0) {
            return "";
        }
        String rest = url.substring(start + 3);
        int at = rest.lastIndexOf('@');
        if (at >= 0) {
            rest = rest.substring(at + 1);
        }
        int cut = indexOfAny(rest, "/?#");
        if (cut >= 0) {
            rest = rest.substring(0, cut);
        }
        if (rest.startsWith("[")) {
            int end = rest.indexOf(']');
            if (end < 0) {
                return "";
            }
            return rest.substring(1, end);
        }
        int colon = rest.lastIndexOf(':');
        if (colon > 0 && rest.indexOf(':') == colon) {
            rest = rest.substring(0, colon);
        }
        return rest;
    }

    private static int indexOfAny(String text, String chars) {
        int best = -1;
        for (int i = 0; i < chars.length(); i++) {
            int at = text.indexOf(chars.charAt(i));
            if (at >= 0 && (best < 0 || at < best)) {
                best = at;
            }
        }
        return best;
    }

    private static String stripBrackets(String host) {
        if (host.startsWith("[") && host.endsWith("]") && host.length() > 2) {
            return host.substring(1, host.length() - 1);
        }
        return host;
    }

    private static String stripDot(String host) {
        if (host.endsWith(".")) {
            return host.substring(0, host.length() - 1);
        }
        return host;
    }

    private static boolean looksLikeIp(String host) {
        if (host.indexOf(':') >= 0) {
            return true;
        }
        int dots = 0;
        int digits = 0;
        for (int i = 0; i < host.length(); i++) {
            char ch = host.charAt(i);
            if (ch == '.') {
                if (digits == 0) {
                    return false;
                }
                dots++;
                digits = 0;
                continue;
            }
            if (ch < '0' || ch > '9') {
                return false;
            }
            digits++;
            if (digits > 3) {
                return false;
            }
        }
        return dots == 3 && digits > 0;
    }
}
