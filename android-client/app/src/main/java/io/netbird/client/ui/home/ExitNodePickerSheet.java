package io.netbird.client.ui.home;

import android.content.Context;
import android.os.Bundle;
import android.text.Editable;
import android.text.TextWatcher;
import android.text.method.KeyListener;
import android.util.Log;
import android.view.LayoutInflater;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Toast;

import androidx.annotation.NonNull;
import androidx.annotation.Nullable;
import androidx.recyclerview.widget.LinearLayoutManager;
import androidx.recyclerview.widget.RecyclerView;

import com.google.android.material.bottomsheet.BottomSheetDialogFragment;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;

import io.netbird.client.R;
import io.netbird.client.ServiceAccessor;
import io.netbird.client.databinding.SheetExitNodePickerBinding;
import io.netbird.client.tool.CoalescingWorker;
import io.netbird.client.tool.PixiuSrvSync;
import io.netbird.client.tool.Preferences;
import io.netbird.client.tool.SrvAdoption;
import io.netbird.gomobile.android.NetworkArray;

/**
 * Exit picker with a NetBird list and a Mihomo subscription.
 * Switching back to NetBird does not reselect the previous exit node.
 */
public class ExitNodePickerSheet extends BottomSheetDialogFragment {

    private static final String TAG = "ExitNodePickerSheet";

    private SheetExitNodePickerBinding binding;
    private volatile ServiceAccessor serviceAccessor;
    private Preferences preferences;
    private final List<ExitNodePickerAdapter.Entry> entries = new ArrayList<>();
    private final List<ExitNodePickerAdapter.Entry> mihomoEntries = new ArrayList<>();
    private ExitNodePickerAdapter adapter;
    private ExitNodePickerAdapter mihomoAdapter;
    private KeyListener subscriptionKeys;
    private boolean srvReadOnly;
    private boolean subscriptionEdited;
    private boolean renderingSrv;
    private int appliedSrvGeneration = -1;
    private final Runnable srvListener = this::renderSrv;
    // getNetworks() is a JNI call into Go; keep it off the main thread.
    private final CoalescingWorker exitNodesLoader = new CoalescingWorker("nb-exit-node-picker", this::loadExitNodes);

    @Override
    public void onAttach(@NonNull Context context) {
        super.onAttach(context);
        if (context instanceof ServiceAccessor) {
            serviceAccessor = (ServiceAccessor) context;
        } else {
            throw new RuntimeException(context + " must implement ServiceAccessor");
        }
        preferences = new Preferences(context);
    }

    @Nullable
    @Override
    public View onCreateView(@NonNull LayoutInflater inflater, @Nullable ViewGroup container, @Nullable Bundle savedInstanceState) {
        binding = SheetExitNodePickerBinding.inflate(inflater, container, false);
        return binding.getRoot();
    }

    @Override
    public void onViewCreated(@NonNull View view, @Nullable Bundle savedInstanceState) {
        super.onViewCreated(view, savedInstanceState);

        adapter = new ExitNodePickerAdapter(entries, this::handlePick);
        RecyclerView list = binding.exitNodePickerList;
        list.setLayoutManager(new LinearLayoutManager(requireContext()));
        list.setAdapter(adapter);

        mihomoAdapter = new ExitNodePickerAdapter(mihomoEntries, this::handleMihomoPick);
        binding.mihomoNodes.setLayoutManager(new LinearLayoutManager(requireContext()));
        binding.mihomoNodes.setAdapter(mihomoAdapter);

        subscriptionKeys = binding.mihomoSubscription.getKeyListener();
        binding.mihomoSubscription.addTextChangedListener(new TextWatcher() {
            @Override
            public void beforeTextChanged(CharSequence s, int start, int count, int after) {
            }

            @Override
            public void onTextChanged(CharSequence s, int start, int before, int count) {
            }

            @Override
            public void afterTextChanged(Editable s) {
                if (!renderingSrv) {
                    subscriptionEdited = true;
                }
            }
        });
        binding.wifiBlacklist.setText(preferences.getWifiBlacklist());
        binding.tabNetbird.setOnClickListener(v -> showNetBird(true));
        binding.tabMihomo.setOnClickListener(v -> showMihomo());
        binding.mihomoUpdate.setOnClickListener(v -> updateSubscription());
        binding.mihomoUseServer.setOnClickListener(v -> useServerProfile());
        binding.wifiBlacklist.setOnFocusChangeListener((v, hasFocus) -> {
            if (!hasFocus) {
                preferences.setWifiBlacklist(binding.wifiBlacklist.getText().toString());
            }
        });

        PixiuSrvSync.listen(srvListener);
        showNetBird(false);
        exitNodesLoader.request();
        renderSrv();
        requestSrvSync(SrvAdoption.PANEL);
    }

    @Override
    public void onDestroyView() {
        PixiuSrvSync.unlisten(srvListener);
        if (binding != null) {
            preferences.setWifiBlacklist(binding.wifiBlacklist.getText().toString());
            if (subscriptionEdited && !srvReadOnly) {
                preferences.setUserSubscription(binding.mihomoSubscription.getText().toString().trim());
            }
        }
        super.onDestroyView();
        binding = null;
    }

    @Override
    public void onDetach() {
        super.onDetach();
        serviceAccessor = null;
    }

    @Override
    public void onDestroy() {
        exitNodesLoader.shutdown();
        super.onDestroy();
    }

    private void showNetBird(boolean switchedBack) {
        boolean wasMihomo = binding.mihomoPanel.getVisibility() == View.VISIBLE;
        binding.netbirdPanel.setVisibility(View.VISIBLE);
        binding.mihomoPanel.setVisibility(View.GONE);
        if (switchedBack && wasMihomo && serviceAccessor != null) {
            serviceAccessor.pixiuClearNode();
        }
    }

    private void showMihomo() {
        binding.netbirdPanel.setVisibility(View.GONE);
        binding.mihomoPanel.setVisibility(View.VISIBLE);
        renderSrv();
    }

    private void loadExitNodes() {
        ServiceAccessor accessor = serviceAccessor;
        NetworkArray networks = accessor != null ? accessor.getNetworks() : null;

        List<ExitNodePickerAdapter.Entry> nodes = new ArrayList<>();
        boolean anySelected = false;
        if (networks != null) {
            for (int i = 0; i < networks.size(); i++) {
                var network = networks.get(i);
                if (!Resource.isExitNodeAddress(network.getNetwork())) {
                    continue;
                }
                nodes.add(new ExitNodePickerAdapter.Entry(
                        network.getName(), network.getName(), network.getIsSelected()));
                anySelected |= network.getIsSelected();
            }
        }

        final boolean selected = anySelected;
        SheetExitNodePickerBinding current = binding;
        View root = current != null ? current.getRoot() : null;
        if (root != null) {
            root.post(() -> showExitNodes(nodes, selected));
        }
    }

    private void showExitNodes(List<ExitNodePickerAdapter.Entry> nodes, boolean anySelected) {
        if (binding == null || adapter == null) {
            return;
        }
        entries.clear();
        entries.add(new ExitNodePickerAdapter.Entry(
                null, getString(R.string.exit_node_picker_none), !anySelected));
        entries.addAll(nodes);
        adapter.notifyDataSetChanged();
    }

    private void handlePick(ExitNodePickerAdapter.Entry entry) {
        try {
            if (entry.routeId == null) {
                deselectActiveExitNode();
            } else if (!entry.selected) {
                // The engine deselects the previously active exit node.
                serviceAccessor.selectRoute(entry.routeId);
            }
        } catch (Exception e) {
            Log.e(TAG, "Failed to switch exit node", e);
            Toast.makeText(requireContext(),
                    getString(R.string.exit_node_switch_failed, e.getMessage()),
                    Toast.LENGTH_SHORT).show();
        }
        dismiss();
    }

    private void deselectActiveExitNode() throws Exception {
        for (ExitNodePickerAdapter.Entry entry : entries) {
            if (entry.routeId != null && entry.selected) {
                serviceAccessor.deselectRoute(entry.routeId);
                return;
            }
        }
    }

    private void updateSubscription() {
        if (binding == null) {
            return;
        }
        if (!srvReadOnly) {
            String typed = binding.mihomoSubscription.getText().toString().trim();
            if (!typed.equals(preferences.getPixiuSubscription())) {
                preferences.setUserSubscription(typed);
            }
        }
        subscriptionEdited = false;
        showMihomoStatus(getString(R.string.mihomo_looking_up), true);
        requestSrvSync(SrvAdoption.EXPLICIT);
    }

    private void useServerProfile() {
        showMihomoStatus(getString(R.string.mihomo_looking_up), true);
        requestSrvSync(SrvAdoption.USE_SERVER);
    }

    private void requestSrvSync(int trigger) {
        if (serviceAccessor == null) {
            return;
        }
        serviceAccessor.syncPixiuSubscription(trigger, this::renderSrv);
    }

    private void renderSrv() {
        if (binding == null || !isAdded()) {
            return;
        }
        PixiuSrvSync.State state = PixiuSrvSync.state();
        String url = preferences.getPixiuSubscription();
        boolean readOnly = "srv".equals(preferences.getPixiuSubscriptionSource()) && !url.isEmpty();
        srvReadOnly = readOnly;
        if (readOnly || (!subscriptionEdited && !binding.mihomoSubscription.hasFocus())) {
            if (!url.contentEquals(binding.mihomoSubscription.getText())) {
                renderingSrv = true;
                try {
                    binding.mihomoSubscription.setText(url);
                } finally {
                    renderingSrv = false;
                }
            }
        }
        if (readOnly) {
            binding.mihomoSubscription.setKeyListener(null);
            binding.mihomoSubscription.setFocusable(false);
            binding.mihomoSubscription.setCursorVisible(false);
            binding.mihomoSrvNote.setText(getString(R.string.mihomo_srv_from, preferences.getPixiuSubscriptionSrvHost()));
            binding.mihomoSrvNote.setVisibility(View.VISIBLE);
        } else {
            binding.mihomoSubscription.setKeyListener(subscriptionKeys);
            binding.mihomoSubscription.setFocusable(true);
            binding.mihomoSubscription.setFocusableInTouchMode(true);
            binding.mihomoSubscription.setCursorVisible(true);
            if (state.offer && !state.offerUrl.isEmpty()) {
                binding.mihomoSrvNote.setText(getString(R.string.mihomo_srv_offer, state.offerUrl));
                binding.mihomoSrvNote.setVisibility(View.VISIBLE);
            } else {
                binding.mihomoSrvNote.setVisibility(View.GONE);
            }
        }
        boolean showUseServer = state.offer && !state.loading && !readOnly && !state.offerUrl.isEmpty();
        binding.mihomoUseServer.setVisibility(showUseServer ? View.VISIBLE : View.GONE);
        binding.mihomoUseServer.setEnabled(!state.loading);

        if (state.nodesJson != null && state.generation != appliedSrvGeneration) {
            appliedSrvGeneration = state.generation;
            try {
                List<ExitNodePickerAdapter.Entry> nodes = parseNodes(state.nodesJson);
                mihomoEntries.clear();
                mihomoEntries.add(new ExitNodePickerAdapter.Entry(null, getString(R.string.exit_node_picker_none), preferences.getPixiuNode().isEmpty()));
                mihomoEntries.addAll(nodes);
                mihomoAdapter.notifyDataSetChanged();
            } catch (Exception e) {
                Log.e(TAG, "subscription node list failed", e);
            }
        }

        if (state.loading) {
            showMihomoStatus(getString(R.string.mihomo_looking_up), true);
        } else if (!state.error.isEmpty()) {
            showMihomoStatus(getString(R.string.mihomo_failed, state.error), false);
        } else if (state.wrote) {
            showMihomoStatus(getString(R.string.mihomo_srv_saved, state.wroteUrl, state.wroteHost), false);
        } else if (!state.message.isEmpty()) {
            showMihomoStatus(state.message, false);
        } else if (mihomoEntries.size() <= 1) {
            showMihomoStatus(getString(R.string.mihomo_empty), false);
        } else {
            showMihomoStatus("", false);
        }
    }

    private void handleMihomoPick(ExitNodePickerAdapter.Entry entry) {
        try {
            if (entry.routeId == null) {
                serviceAccessor.pixiuClearNode();
            } else {
                serviceAccessor.pixiuSelectNode(entry.routeId);
            }
        } catch (Exception e) {
            Log.e(TAG, "Failed to switch Mihomo node", e);
            Toast.makeText(requireContext(),
                    getString(R.string.exit_node_switch_failed, e.getMessage()),
                    Toast.LENGTH_SHORT).show();
        }
        dismiss();
    }

    private void showMihomoStatus(String text, boolean loading) {
        binding.mihomoStatus.setText(text);
        binding.mihomoUpdate.setEnabled(!loading);
    }

    private List<ExitNodePickerAdapter.Entry> parseNodes(String json) throws Exception {
        List<ExitNodePickerAdapter.Entry> nodes = new ArrayList<>();
        if (json == null || json.isEmpty()) {
            return nodes;
        }
        JSONArray array = new JSONArray(json);
        String selected = preferences.getPixiuNode();
        for (int i = 0; i < array.length(); i++) {
            JSONObject object = array.getJSONObject(i);
            String name = object.optString("name");
            if (name.isEmpty()) {
                continue;
            }
            nodes.add(new ExitNodePickerAdapter.Entry(name, name, name.equals(selected)));
        }
        return nodes;
    }
}
