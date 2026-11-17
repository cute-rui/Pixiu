package io.netbird.client.tool;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.os.Build;

import java.net.InetAddress;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;

/** DNS servers on networks that are not the NetBird VPN. */
public final class PhysicalDns {

    private PhysicalDns() {
    }

    public static List<String> servers(Context context) {
        LinkedHashSet<String> out = new LinkedHashSet<>();
        ConnectivityManager connectivity = (ConnectivityManager) context.getSystemService(Context.CONNECTIVITY_SERVICE);
        if (connectivity == null) {
            return new ArrayList<>();
        }
        Network[] networks = connectivity.getAllNetworks();
        if (networks == null) {
            return new ArrayList<>();
        }
        for (Network network : networks) {
            NetworkCapabilities caps = connectivity.getNetworkCapabilities(network);
            if (caps == null || caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) {
                continue;
            }
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP
                    && !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)) {
                continue;
            }
            LinkProperties properties = connectivity.getLinkProperties(network);
            if (properties == null) {
                continue;
            }
            for (InetAddress address : properties.getDnsServers()) {
                if (address == null || rejected(address)) {
                    continue;
                }
                String host = address.getHostAddress();
                if (host == null || host.isEmpty()) {
                    continue;
                }
                int zone = host.indexOf('%');
                if (zone >= 0) {
                    host = host.substring(0, zone);
                }
                out.add(host);
            }
        }
        return new ArrayList<>(out);
    }

    static boolean rejected(InetAddress address) {
        if (address.isAnyLocalAddress() || address.isLoopbackAddress()
                || address.isLinkLocalAddress() || address.isMulticastAddress()) {
            return true;
        }
        byte[] raw = address.getAddress();
        if (raw == null) {
            return true;
        }
        if (raw.length == 16 && isV4Mapped(raw)) {
            byte[] v4 = new byte[]{raw[12], raw[13], raw[14], raw[15]};
            return rejectedV4(v4) || rejectedV6(raw);
        }
        if (raw.length == 4) {
            return rejectedV4(raw);
        }
        if (raw.length == 16) {
            return rejectedV6(raw);
        }
        return true;
    }

    private static boolean isV4Mapped(byte[] raw) {
        for (int i = 0; i < 10; i++) {
            if (raw[i] != 0) {
                return false;
            }
        }
        return raw[10] == (byte) 0xff && raw[11] == (byte) 0xff;
    }

    private static boolean rejectedV4(byte[] raw) {
        int first = raw[0] & 0xff;
        int second = raw[1] & 0xff;
        return first == 100 && second >= 64 && second <= 127;
    }

    private static boolean rejectedV6(byte[] raw) {
        return (raw[0] & 0xfe) == 0xfc;
    }
}
