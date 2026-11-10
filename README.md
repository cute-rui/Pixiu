# Pixiu

Pixiu is a NetBird client with Mihomo as the other exit, plus the Pixiu desktop app and a `pixiu` CLI.

The root `LICENSE` is the upstream NetBird BSD-3-Clause text. `management/`, `signal/`, `relay/`, and `combined/` inside `netbird/` stay AGPL-3.0, as that file says. Each vendored tree keeps its own upstream license.

## Layout

- `netbird/` — NetBird. The TUN keeps overlay prefixes (`100.64.0.0/10` and IPv6 ULA) and diverts everything else to Mihomo.
- `bridge/` — `github.com/pixiu/mihomo-bridge`, pinned to Mihomo `v1.19.32`. NetBird requires it with `replace => ../bridge`.
- `android-client/` and `ios-client/` — mobile apps. `android-client/netbird` and `ios-client/netbird-core` are symlinks to `../netbird`. tvOS has no Mihomo.
- `verge/` — the Pixiu desktop app. The NetBird page reads the local daemon over its unix socket or Windows named pipe and merges peer public addresses into the runtime config only.
- `cli/` — the `pixiu` binary. Config is `~/.config/pixiu.toml`.

## Build

Go 1.26.7:

```sh
export PATH="/usr/local/go/bin:$PATH"
export GOPROXY=https://proxy.golang.org,direct
cd bridge && go test ./...
cd ../netbird && go test -run 'Overlay|PacketDestination|ReadDiverts' ./client/iface/device/
cd ../cli && go test ./... && go build -o pixiu .
```

Verge uses Rust 1.99.1 (`verge/rust-toolchain.toml`, crate `rust-version` 1.99). From `verge/`:

```sh
pnpm install
pnpm prebuild -- x86_64-unknown-linux-gnu
pnpm exec tauri build
```

Android (JDK 17, NDK `23.1.7779620`):

```sh
cd android-client
./build-android-lib.sh
./gradlew assembleDebug
```

## Installers

Pull requests, manual runs, and pushes to branches other than `main` upload installers from the `verge`, `go`, and `android` workflows:

| Actions artifact | Installer |
| --- | --- |
| `pixiu-desktop-linux-amd64` | `.deb` and `.rpm` |
| `pixiu-desktop-macos-arm64` | `.dmg` |
| `pixiu-desktop-windows-amd64` | NSIS `*setup*.exe` |
| `pixiu-cli-linux-amd64` | `pixiu-linux-amd64` |
| `pixiu-cli-darwin-arm64` | `pixiu-darwin-arm64` |
| `pixiu-cli-windows-amd64` | `pixiu-windows-amd64.exe` |
| `pixiu-android-apk` | debug `pixiu-android.apk` |

A push to `main`, or **Actions → prerelease → Run workflow** on `main`, builds those same installers once and replaces the rolling prerelease. The tag is `prerelease` and the title is `Pixiu main prerelease`. It stays a prerelease and is not marked Latest. If `TAURI_SIGNING_PRIVATE_KEY` is unset, the Verge build is unsigned and still succeeds.

## CLI

`pixiu` follows mihoro: `init`, `update`, `apply`, `start`, `status`, `stop`, `restart`, `log`, `proxy`, `uninstall`, `completions`, `cron`, `upgrade`.

`apply` and `start` write a runtime config. When the NetBird daemon is up they merge its public ICE and relay addresses. When it is down they print `netbird daemon is not running` and continue.

`wifi_blacklist` in `pixiu.toml` is a list of SSIDs. An empty list, another SSID, ethernet, or a failed scan does not block. A listed SSID does not auto-connect. `pixiu start` warns and waits for `--yes`.
