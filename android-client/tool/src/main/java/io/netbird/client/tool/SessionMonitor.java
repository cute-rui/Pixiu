package io.netbird.client.tool;

import android.os.Handler;
import android.os.Looper;
import android.util.Log;

import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.LongSupplier;
import java.util.function.Supplier;

/**
 * Fans session notifications out to the app. The Go client only wakes this
 * class with {@link #onStateChanged()}; it then pulls Status / deadline and
 * arms T-10 and T-2 warnings locally, matching the desktop session watcher.
 *
 * <p>All state lives on the main thread; {@link #onStateChanged()} may be
 * called from any thread.
 */
public class SessionMonitor {

    /** Run-loop status label meaning an interactive login is required. */
    private static final String STATUS_NEEDS_LOGIN = "NeedsLogin";

    private static final long WARNING_LEAD_SECONDS = 10 * 60;
    private static final long FINAL_WARNING_LEAD_SECONDS = 2 * 60;

    private static final String LOGTAG = "SessionMonitor";

    private final Handler handler = new Handler(Looper.getMainLooper());
    private final Supplier<String> status;
    private final LongSupplier deadlineUnixSeconds;
    private final Set<SessionEventListener> listeners = ConcurrentHashMap.newKeySet();

    private long lastDeadline;
    private boolean wasNeedsLogin;
    private long warningArmedFor;
    private long finalArmedFor;
    private final Runnable warningRunnable = () -> fireWarning(warningArmedFor, 10, false);
    private final Runnable finalRunnable = () -> fireWarning(finalArmedFor, 2, true);

    public SessionMonitor(Supplier<String> status, LongSupplier deadlineUnixSeconds) {
        this.status = status;
        this.deadlineUnixSeconds = deadlineUnixSeconds;
    }

    /** Wake-up from the Go state-change signal. Safe to call from any thread. */
    public void onStateChanged() {
        handler.post(this::refresh);
    }

    public void addListener(SessionEventListener listener) {
        listeners.add(listener);
        // Replay the current state so a late-binding UI doesn't miss what
        // happened while it was detached. Both cases are real: the activity
        // unbinds for the SSO browser round-trip (which is when an extend
        // moves the deadline), and a session can expire with no UI running at
        // all — the expiry is an edge the listener would never see otherwise.
        // Same pattern as the Go notifier's setListener.
        handler.post(() -> {
            refresh();
            notifySafely(() -> listener.onSessionDeadlineChanged(lastDeadline));
            if (isLoginRequired()) {
                notifySafely(listener::onSessionExpired);
            }
        });
    }

    /** True while the run loop reports that an interactive login is needed. */
    public boolean isLoginRequired() {
        return STATUS_NEEDS_LOGIN.equals(status.get());
    }

    public void removeListener(SessionEventListener listener) {
        listeners.remove(listener);
    }

    private void refresh() {
        long deadline = deadlineUnixSeconds.getAsLong();
        if (deadline != lastDeadline) {
            lastDeadline = deadline;
            for (SessionEventListener l : listeners) {
                notifySafely(() -> l.onSessionDeadlineChanged(deadline));
            }
            rescheduleWarnings(deadline);
        }

        boolean needsLogin = STATUS_NEEDS_LOGIN.equals(status.get());
        if (needsLogin && !wasNeedsLogin) {
            for (SessionEventListener l : listeners) {
                notifySafely(l::onSessionExpired);
            }
        }
        wasNeedsLogin = needsLogin;
    }

    private void rescheduleWarnings(long deadline) {
        handler.removeCallbacks(warningRunnable);
        handler.removeCallbacks(finalRunnable);
        warningArmedFor = 0;
        finalArmedFor = 0;
        if (deadline <= 0) {
            return;
        }
        long now = System.currentTimeMillis() / 1000L;
        if (deadline <= now) {
            return;
        }
        long warnAt = deadline - WARNING_LEAD_SECONDS;
        long finalAt = deadline - FINAL_WARNING_LEAD_SECONDS;
        if (now < warnAt) {
            warningArmedFor = deadline;
            handler.postDelayed(warningRunnable, (warnAt - now) * 1000L);
        } else if (now < finalAt) {
            fireWarning(deadline, 10, false);
        }
        if (now < finalAt) {
            finalArmedFor = deadline;
            handler.postDelayed(finalRunnable, (finalAt - now) * 1000L);
        } else {
            fireWarning(deadline, 2, true);
        }
    }

    private void fireWarning(long deadline, long leadMinutes, boolean finalWarning) {
        if (deadline == 0 || deadline != lastDeadline) {
            return;
        }
        for (SessionEventListener l : listeners) {
            notifySafely(() -> l.onSessionExpiring(deadline, leadMinutes, finalWarning));
        }
    }

    private void notifySafely(Runnable r) {
        try {
            r.run();
        } catch (Exception e) {
            Log.w(LOGTAG, "session listener failed", e);
        }
    }
}
