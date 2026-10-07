package io.netbird.client.ui.home;

import android.content.Context;
import android.os.Bundle;
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
import io.netbird.client.tool.Preferences;
import io.netbird.gomobile.android.NetworkArray;

/**
 * Exit picker with a NetBird list and a Mihomo subscription.
 * Switching back to NetBird does not reselect the previous exit node.
 */
public class ExitNodePickerSheet extends BottomSheetDialogFragment {

    private static final String TAG = "ExitNodePickerSheet";

    private SheetExitNodePickerBinding binding;
    private ServiceAccessor serviceAccessor;
    private Preferences preferences;
    private final List<ExitNodePickerAdapter.Entry> entries = new ArrayList<>();
    private final List<ExitNodePickerAdapter.Entry> mihomoEntries = new ArrayList<>();
    private ExitNodePickerAdapter adapter;
    private ExitNodePickerAdapter mihomoAdapter;

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

        binding.mihomoSubscription.setText(preferences.getPixiuSubscription());
        binding.wifiBlacklist.setText(preferences.getWifiBlacklist());
        binding.tabNetbird.setOnClickListener(v -> showNetBird(true));
        binding.tabMihomo.setOnClickListener(v -> showMihomo());
        binding.mihomoUpdate.setOnClickListener(v -> updateSubscription());
        binding.wifiBlacklist.setOnFocusChangeListener((v, hasFocus) -> {
            if (!hasFocus) {
                preferences.setWifiBlacklist(binding.wifiBlacklist.getText().toString());
            }
        });

        showNetBird(false);
        loadExitNodes();
        showMihomoStatus(getString(R.string.mihomo_empty), false);
    }

    @Override
    public void onDestroyView() {
        if (binding != null) {
            preferences.setWifiBlacklist(binding.wifiBlacklist.getText().toString());
            preferences.setPixiuSubscription(binding.mihomoSubscription.getText().toString().trim());
        }
        super.onDestroyView();
        binding = null;
    }

    @Override
    public void onDetach() {
        super.onDetach();
        serviceAccessor = null;
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
    }

    private void loadExitNodes() {
        NetworkArray networks = serviceAccessor != null ? serviceAccessor.getNetworks() : null;

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
        String url = binding.mihomoSubscription.getText().toString().trim();
        preferences.setPixiuSubscription(url);
        showMihomoStatus(getString(R.string.mihomo_loading), true);
        new Thread(() -> {
            try {
                String json = serviceAccessor.pixiuUpdateSubscription(url);
                List<ExitNodePickerAdapter.Entry> nodes = parseNodes(json);
                if (!isAdded()) {
                    return;
                }
                requireActivity().runOnUiThread(() -> {
                    if (binding == null) {
                        return;
                    }
                    mihomoEntries.clear();
                    mihomoEntries.add(new ExitNodePickerAdapter.Entry(null, getString(R.string.exit_node_picker_none), preferences.getPixiuNode().isEmpty()));
                    mihomoEntries.addAll(nodes);
                    mihomoAdapter.notifyDataSetChanged();
                    if (nodes.isEmpty()) {
                        showMihomoStatus(getString(R.string.mihomo_empty), false);
                    } else {
                        showMihomoStatus("", false);
                    }
                });
            } catch (Exception e) {
                Log.e(TAG, "subscription update failed", e);
                if (!isAdded()) {
                    return;
                }
                requireActivity().runOnUiThread(() -> {
                    if (binding == null) {
                        return;
                    }
                    showMihomoStatus(getString(R.string.mihomo_failed, e.getMessage()), false);
                });
            }
        }).start();
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
