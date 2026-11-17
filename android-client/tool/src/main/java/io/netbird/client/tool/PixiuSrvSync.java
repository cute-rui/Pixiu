package io.netbird.client.tool;

import android.content.Context;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * Looks up _pixiu._tcp when the management hostname is first known and when it
 * changes. The subscription slot stays a single URL.
 */
public final class PixiuSrvSync {

    private static final String TAG = "PixiuSrv";
    private static final long PANEL_WINDOW_MS = 5 * 60 * 1000L;

    public interface Backend {
        String discover(String managementURL, String dnsServers) throws Exception;

        String update(String url) throws Exception;
    }

    public static final class State {
        public final boolean loading;
        public final boolean offer;
        public final String offerUrl;
        public final String message;
        public final String error;
        public final String nodesJson;
        public final boolean wrote;
        public final String wroteUrl;
        public final String wroteHost;
        public final int generation;

        State(boolean loading, boolean offer, String offerUrl, String message, String error,
              String nodesJson, boolean wrote, String wroteUrl, String wroteHost, int generation) {
            this.loading = loading;
            this.offer = offer;
            this.offerUrl = offerUrl == null ? "" : offerUrl;
            this.message = message == null ? "" : message;
            this.error = error == null ? "" : error;
            this.nodesJson = nodesJson;
            this.wrote = wrote;
            this.wroteUrl = wroteUrl == null ? "" : wroteUrl;
            this.wroteHost = wroteHost == null ? "" : wroteHost;
            this.generation = generation;
        }

        static State idle() {
            return new State(false, false, "", "", "", null, false, "", "", 0);
        }

        State loading() {
            return new State(true, offer, offerUrl, message, "", null, false, "", "", generation);
        }
    }

    private static final ExecutorService EXEC = Executors.newSingleThreadExecutor(runnable -> {
        Thread thread = new Thread(runnable, "pixiu-srv");
        thread.setDaemon(true);
        return thread;
    });
    private static final List<Runnable> LISTENERS = new CopyOnWriteArrayList<>();
    private static final Object LOCK = new Object();
    private static final Handler MAIN = new Handler(Looper.getMainLooper());

    private static State state = State.idle();
    private static String armedHost;
    private static String conclusiveHost = "";
    private static long conclusiveAt;
    private static boolean nodesLoaded;
    private static int generation;

    private PixiuSrvSync() {
    }

    public static void listen(Runnable listener) {
        LISTENERS.add(listener);
    }

    public static void unlisten(Runnable listener) {
        LISTENERS.remove(listener);
    }

    public static State state() {
        synchronized (LOCK) {
            return state;
        }
    }

    public static void submit(Context context, Backend backend, Runnable clearNode, int trigger, Runnable done) {
        Context app = context.getApplicationContext();
        EXEC.execute(() -> {
            try {
                run(app, backend, clearNode, trigger);
            } catch (Exception e) {
                Log.e(TAG, "subscription lookup failed", e);
                synchronized (LOCK) {
                    generation++;
                    state = new State(false, false, "", "", e.getMessage() == null ? "" : e.getMessage(),
                            null, false, "", "", generation);
                }
            }
            notifyListeners();
            if (done != null) {
                MAIN.post(done);
            }
        });
    }

    private static void run(Context context, Backend backend, Runnable clearNode, int trigger) throws Exception {
        String management = readManagement(context);
        String host = SrvAdoption.managementHost(management);
        boolean query;
        synchronized (LOCK) {
            boolean hostChanged = armedHost == null || !armedHost.equals(host);
            boolean explicit = trigger == SrvAdoption.EXPLICIT || trigger == SrvAdoption.USE_SERVER;
            boolean panelDue = trigger == SrvAdoption.PANEL && (hostChanged || !recent(host));
            query = explicit || (trigger == SrvAdoption.AUTO && hostChanged) || panelDue;
            if (query) {
                state = state.loading();
            }
        }
        if (!query) {
            return;
        }
        notifyListeners();

        String dns = join(PhysicalDns.servers(context));
        SrvAdoption.Lookup lookup;
        try {
            lookup = SrvAdoption.Lookup.parse(backend.discover(management, dns));
        } catch (Exception e) {
            Log.w(TAG, "SRV lookup failed", e);
            lookup = SrvAdoption.Lookup.failure(e.getMessage());
        }

        Preferences preferences = new Preferences(context);
        boolean loaded;
        synchronized (LOCK) {
            loaded = nodesLoaded;
        }
        SrvAdoption.Decision decision = SrvAdoption.decide(
                preferences.getPixiuSubscription(),
                preferences.getPixiuSubscriptionSource(),
                loaded,
                lookup,
                trigger);

        String nodesJson = null;
        String error = "";
        boolean wrote = false;
        String wroteUrl = "";
        if (!decision.fetchUrls.isEmpty()) {
            for (String url : decision.fetchUrls) {
                try {
                    String json = backend.update(url);
                    synchronized (LOCK) {
                        nodesLoaded = true;
                    }
                    nodesJson = json == null ? "[]" : json;
                    if (decision.writeSrv) {
                        preferences.setSrvSubscription(url, lookup.host);
                        wrote = true;
                        wroteUrl = url;
                    }
                    dropMissingNode(preferences, nodesJson, clearNode);
                    error = "";
                    break;
                } catch (Exception e) {
                    error = e.getMessage() == null ? "" : e.getMessage();
                    Log.w(TAG, "subscription fetch failed for " + url, e);
                }
            }
        }

        synchronized (LOCK) {
            if (isConclusive(lookup.kind)) {
                armedHost = host;
                conclusiveHost = host;
                conclusiveAt = System.currentTimeMillis();
            }
            generation++;
            String message = error.isEmpty() ? decision.message : "";
            state = new State(false, decision.showOffer, decision.offerUrl, message, error,
                    nodesJson, wrote, wroteUrl, lookup.host, generation);
        }
    }

    private static boolean recent(String host) {
        return host.equals(conclusiveHost) && System.currentTimeMillis() - conclusiveAt < PANEL_WINDOW_MS;
    }

    private static boolean isConclusive(String kind) {
        return SrvAdoption.KIND_URL.equals(kind)
                || SrvAdoption.KIND_MISSING.equals(kind)
                || SrvAdoption.KIND_REJECTED.equals(kind);
    }

    private static void dropMissingNode(Preferences preferences, String json, Runnable clearNode) {
        String selected = preferences.getPixiuNode();
        if (selected == null || selected.isEmpty()) {
            return;
        }
        try {
            JSONArray array = new JSONArray(json);
            for (int i = 0; i < array.length(); i++) {
                JSONObject node = array.optJSONObject(i);
                if (node != null && selected.equals(node.optString("name"))) {
                    return;
                }
            }
        } catch (Exception e) {
            Log.w(TAG, "could not read the node list", e);
            return;
        }
        preferences.setPixiuNode("");
        if (clearNode != null) {
            clearNode.run();
        }
    }

    static String readManagement(Context context) {
        try {
            String path = new ProfileManagerWrapper(context).getActiveConfigPath();
            io.netbird.gomobile.android.Preferences goPreferences = MDMBridge.openPreferences(context, path);
            String url = goPreferences.getManagementURL();
            if (url == null || url.trim().isEmpty()) {
                return "https://api.netbird.io";
            }
            return url.trim();
        } catch (Exception e) {
            Log.w(TAG, "management URL is not readable", e);
            return "";
        }
    }

    private static String join(List<String> servers) {
        StringBuilder out = new StringBuilder();
        for (String server : servers) {
            if (server == null || server.isEmpty()) {
                continue;
            }
            if (out.length() > 0) {
                out.append('\n');
            }
            out.append(server);
        }
        return out.toString();
    }

    private static void notifyListeners() {
        MAIN.post(() -> {
            for (Runnable listener : LISTENERS) {
                listener.run();
            }
        });
    }
}
