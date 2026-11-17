package io.netbird.client.tool;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

import java.util.Arrays;
import java.util.Collections;

public class SrvAdoptionTest {

    @Test
    public void managementHostStripsPortAndRejectsLiterals() {
        assertEquals("api.example.com", SrvAdoption.managementHost("https://api.example.com:33073"));
        assertEquals("api.netbird.io", SrvAdoption.managementHost("https://api.netbird.io:443"));
        assertEquals("api.example.com", SrvAdoption.managementHost("https://API.Example.com/path"));
        assertEquals("api.example.com", SrvAdoption.managementHost("https://user:secret@api.example.com"));
        assertEquals("ip", SrvAdoption.managementHost("https://100.64.0.1:443"));
        assertEquals("ip", SrvAdoption.managementHost("https://[fc00::1]:33073"));
        assertEquals("", SrvAdoption.managementHost(""));
        assertEquals("", SrvAdoption.managementHost("://"));
        assertEquals("xn--mnchen-3ya.example", SrvAdoption.managementHost("https://münchen.example"));
    }

    @Test
    public void legacyUrlIsTreatedAsUser() {
        assertEquals(SrvAdoption.SOURCE_USER, SrvAdoption.effectiveSource("https://mine.example/pixiu.yaml", ""));
        assertEquals("", SrvAdoption.effectiveSource("", ""));
        assertEquals(SrvAdoption.SOURCE_SRV, SrvAdoption.effectiveSource("https://profile.example/pixiu.yaml", SrvAdoption.SOURCE_SRV));
    }

    @Test
    public void emptySlotIsFilledFromTheRecord() {
        SrvAdoption.Decision decision = SrvAdoption.decide("", "", false, urlLookup(), SrvAdoption.AUTO);
        assertTrue(decision.writeSrv);
        assertEquals(Arrays.asList("https://profile.example/pixiu.yaml", "https://profile.example:8443/pixiu.yaml"), decision.fetchUrls);
        assertFalse(decision.showOffer);
    }

    @Test
    public void userSubscriptionIsNotOverwritten() {
        SrvAdoption.Decision decision = SrvAdoption.decide(
                "https://mine.example/pixiu.yaml", "", false, urlLookup(), SrvAdoption.AUTO);
        assertFalse(decision.writeSrv);
        assertTrue(decision.fetchUrls.isEmpty());
        assertTrue(decision.showOffer);
        assertEquals("https://profile.example/pixiu.yaml", decision.offerUrl);
    }

    @Test
    public void useServerWritesOverAUserSubscription() {
        SrvAdoption.Decision decision = SrvAdoption.decide(
                "https://mine.example/pixiu.yaml", SrvAdoption.SOURCE_USER, true, urlLookup(), SrvAdoption.USE_SERVER);
        assertTrue(decision.writeSrv);
        assertEquals("https://profile.example/pixiu.yaml", decision.fetchUrls.get(0));
        assertFalse(decision.showOffer);
    }

    @Test
    public void missingAndRejectedLeaveTheSlot() {
        for (String kind : new String[]{SrvAdoption.KIND_MISSING, SrvAdoption.KIND_REJECTED}) {
            SrvAdoption.Lookup lookup = new SrvAdoption.Lookup(kind, "api.example.com", "", Collections.<String>emptyList(), kind + " reason");
            SrvAdoption.Decision user = SrvAdoption.decide("https://mine.example/pixiu.yaml", SrvAdoption.SOURCE_USER, true, lookup, SrvAdoption.AUTO);
            assertTrue(user.fetchUrls.isEmpty());
            assertFalse(user.writeSrv);
            assertEquals(kind + " reason", user.message);

            SrvAdoption.Decision srv = SrvAdoption.decide("https://old.example/pixiu.yaml", SrvAdoption.SOURCE_SRV, true, lookup, SrvAdoption.AUTO);
            assertTrue(srv.fetchUrls.isEmpty());
            assertFalse(srv.writeSrv);
            assertEquals(kind + " reason", srv.message);
        }
    }

    @Test
    public void explicitRefreshKeepsTheSavedUrlWhenTheRecordIsMissing() {
        SrvAdoption.Lookup lookup = new SrvAdoption.Lookup(
                SrvAdoption.KIND_MISSING, "api.example.com", "", Collections.<String>emptyList(), "No _pixiu._tcp record on api.example.com. The subscription was left unchanged.");
        SrvAdoption.Decision decision = SrvAdoption.decide(
                "https://old.example/pixiu.yaml", SrvAdoption.SOURCE_SRV, true, lookup, SrvAdoption.EXPLICIT);
        assertEquals(Collections.singletonList("https://old.example/pixiu.yaml"), decision.fetchUrls);
        assertFalse(decision.writeSrv);
        assertTrue(decision.message.contains("left unchanged"));
    }

    @Test
    public void srvSlotFollowsANewRecord() {
        SrvAdoption.Decision decision = SrvAdoption.decide(
                "https://old.example/pixiu.yaml", SrvAdoption.SOURCE_SRV, true, urlLookup(), SrvAdoption.AUTO);
        assertTrue(decision.writeSrv);
        assertEquals("https://profile.example/pixiu.yaml", decision.fetchUrls.get(0));
    }

    @Test
    public void sameSrvUrlIsNotFetchedAgainUntilRefresh() {
        SrvAdoption.Lookup lookup = new SrvAdoption.Lookup(
                SrvAdoption.KIND_URL, "api.example.com", "https://profile.example/pixiu.yaml",
                Collections.singletonList("https://profile.example/pixiu.yaml"), "");
        SrvAdoption.Decision loaded = SrvAdoption.decide(
                "https://profile.example/pixiu.yaml", SrvAdoption.SOURCE_SRV, true, lookup, SrvAdoption.AUTO);
        assertTrue(loaded.fetchUrls.isEmpty());
        SrvAdoption.Decision fresh = SrvAdoption.decide(
                "https://profile.example/pixiu.yaml", SrvAdoption.SOURCE_SRV, false, lookup, SrvAdoption.PANEL);
        assertEquals(Collections.singletonList("https://profile.example/pixiu.yaml"), fresh.fetchUrls);
        assertFalse(fresh.writeSrv);
    }

    private static SrvAdoption.Lookup urlLookup() {
        return new SrvAdoption.Lookup(
                SrvAdoption.KIND_URL,
                "api.example.com",
                "https://profile.example/pixiu.yaml",
                Arrays.asList("https://profile.example/pixiu.yaml", "https://profile.example:8443/pixiu.yaml"),
                "");
    }
}
