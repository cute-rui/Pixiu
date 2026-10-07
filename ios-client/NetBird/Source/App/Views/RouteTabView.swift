//
//  RouteTabView.swift
//  NetBird
//
//  Created by Pascal Fischer on 06.05.24.
//
//  Shared between iOS and tvOS.
//  Uses Screen helper for platform-independent sizing.
//

import SwiftUI

struct RouteTabView: View {
    @EnvironmentObject var viewModel: ViewModel
    #if !os(tvOS)
    @State private var exitKind = "netbird"
    @State private var appliedKind = "netbird"
    @State private var subscription = ""
    @State private var wifiBlacklist = ""
    @State private var status = "No nodes yet. Update the subscription."
    @State private var nodes: [PixiuNode] = []
    @State private var loading = false
    @State private var selectedNode = ""
    #endif
    
    var body: some View {
        ZStack {
            Color("BgMenu")
            VStack {
                #if !os(tvOS)
                Picker("Exit", selection: $exitKind) {
                    Text("NetBird").tag("netbird")
                    Text("Mihomo").tag("mihomo")
                }
                .pickerStyle(.segmented)
                .padding([.horizontal, .top])
                if exitKind == "mihomo" {
                    pixiuPanel
                } else {
                    netbirdRoutes
                }
                #else
                netbirdRoutes
                #endif
            }
        }
        .onAppear {
            self.viewModel.refreshMDMRestrictions()
            self.viewModel.routeViewModel.getRoutes()
            #if !os(tvOS)
            let defaults = UserDefaults(suiteName: GlobalConstants.userPreferencesSuiteName)
            subscription = defaults?.string(forKey: GlobalConstants.keyPixiuSubscription) ?? ""
            wifiBlacklist = defaults?.string(forKey: GlobalConstants.keyWifiBlacklist) ?? ""
            #endif
        }
        #if !os(tvOS)
        .onChange(of: exitKind) { kind in
            let previous = appliedKind
            appliedKind = kind
            if previous == "mihomo" && kind == "netbird" {
                viewModel.networkExtensionAdapter.sendPixiu("PixiuClear") { _ in }
            }
        }
        #endif
    }

    private var netbirdRoutes: some View {
        VStack {
            Spacer(minLength: 0)
            if viewModel.mdmRestrictions.mdm.disableClientRoutes {
                RoutesDisabledByPolicyView()
            } else if viewModel.vpnDisplayState == .connected && viewModel.routeViewModel.routeInfo.count > 0 {
                VStack {
                    RouteSelectionHeader(routeViewModel: viewModel.routeViewModel)
                    RouteListView(viewModel: viewModel, routeViewModel: viewModel.routeViewModel, peerViewModel: viewModel.peerViewModel)
                }
            } else {
                NoRoutesView()
            }
            Spacer(minLength: 0)
        }
    }

    #if !os(tvOS)
    private var pixiuPanel: some View {
        VStack(alignment: .leading, spacing: 8) {
            TextField("Subscription URL", text: $subscription)
                .textFieldStyle(.roundedBorder)
                .autocapitalization(.none)
                .disableAutocorrection(true)
            Button(loading ? "Updating subscription…" : "Update") {
                updateSubscription()
            }
            .disabled(loading)
            Text(status)
                .font(.footnote)
                .foregroundColor(Color("TextSecondary"))
            if nodes.isEmpty && !loading {
                Text("No nodes yet. Update the subscription.")
                    .foregroundColor(Color("TextSecondary"))
            } else {
                List(nodes) { node in
                    Button(node.name) {
                        selectedNode = node.name
                        viewModel.routeViewModel.setExitNode(nil)
                        viewModel.networkExtensionAdapter.sendPixiu("PixiuSelect-\(node.name)") { _ in }
                    }
                }
            }
            Text("Wi-Fi blacklist, one SSID per line")
                .font(.footnote)
            TextEditor(text: $wifiBlacklist)
                .frame(minHeight: 80)
                .onChange(of: wifiBlacklist) { value in
                    UserDefaults(suiteName: GlobalConstants.userPreferencesSuiteName)?
                        .set(value, forKey: GlobalConstants.keyWifiBlacklist)
                }
        }
        .padding()
    }

    private func updateSubscription() {
        let defaults = UserDefaults(suiteName: GlobalConstants.userPreferencesSuiteName)
        defaults?.set(subscription, forKey: GlobalConstants.keyPixiuSubscription)
        loading = true
        status = "Updating subscription…"
        viewModel.networkExtensionAdapter.sendPixiu("PixiuSub:\(subscription)") { response in
            DispatchQueue.main.async {
                loading = false
                guard let response, let data = response.data(using: .utf8),
                      let decoded = try? JSONDecoder().decode([PixiuNode].self, from: data) else {
                    status = "Could not update the subscription."
                    nodes = []
                    return
                }
                nodes = decoded
                status = decoded.isEmpty ? "No nodes yet. Update the subscription." : ""
            }
        }
    }
    #endif
}

#if !os(tvOS)
struct PixiuNode: Decodable, Identifiable {
    var name: String
    var type: String
    var server: String
    var port: Int
    var id: String { name }
}
#endif

struct RoutesDisabledByPolicyView: View {
    var body: some View {
        VStack(spacing: 12) {
            Image(systemName: "lock.fill")
                .font(.largeTitle)
                .foregroundColor(Color("TextSecondary"))
            Text("Network routes are managed by your organization")
                .font(.system(size: 18 * Layout.fontScale))
                .foregroundColor(Color("TextPrimary"))
                .multilineTextAlignment(.center)
        }
        .padding(.horizontal, Screen.width * 0.1)
    }
}

struct RouteSelectionHeader: View {
    @ObservedObject var routeViewModel: RoutesViewModel
    
    var body: some View {
        HStack {
            CustomTextField(placeholder: "search resources", text: self.$routeViewModel.routeFilter, secure: .constant(false))
                .padding([.top, .bottom], 10)
            Menu {
                Button(action: { self.routeViewModel.selectionFilter = "All" }) {
                    Text("All")
                }
                Button(action: { self.routeViewModel.selectionFilter = "Enabled" }) {
                    Text("Enabled")
                }
                Button(action: { self.routeViewModel.selectionFilter = "Disabled" }) {
                    Text("Disabled")
                }
            } label: {
                Image("icon-filter")
                    .padding([.leading, .trailing], 4)
            }
        }.padding([.leading, .trailing])
    }
}


struct RouteListView: View {
    @ObservedObject var viewModel: ViewModel
    @ObservedObject var routeViewModel: RoutesViewModel
    @ObservedObject var peerViewModel: PeerViewModel

    var body: some View {
        ScrollView {
            ForEach(Array(self.routeViewModel.filteredResourceRoutes.enumerated()), id: \.element.id) { index, route in
                RouteCard(route: route, selectedRouteId: $routeViewModel.selectedRouteId, orientationTop: index > 3, routeViewModel: routeViewModel)
                    .zIndex(routeViewModel.selectedRouteId == route.id ? 1 : 0)
                    .opacity(self.routeViewModel.tappedRoute == route ? 0.3 : 1.0)
            }
        }
    }
}

struct NoRoutesView: View {
    var body: some View {
        EmptyTabPlaceholder(
            message: "It looks like there are no resources that you can connect to...",
            learnMoreURL: URL(string: "https://docs.netbird.io/how-to/networks")
        )
    }
}
