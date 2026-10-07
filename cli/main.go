package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const version = "0.1.0"

var (
	queryNetBird = queryDaemon
	scanCurrent  = scanOS
	stderr       io.Writer
)

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(args []string, stdout, stderrOut io.Writer) int {
	stderr = stderrOut
	root := newRoot()
	root.SetOut(stdout)
	root.SetErr(stderrOut)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderrOut, err)
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "pixiu",
		Short:         "Run Mihomo beside NetBird",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(
		newInit(),
		newUpdate(),
		newApply(),
		newStart(),
		newStatus(),
		newStop(),
		newRestart(),
		newLog(),
		newProxy(),
		newUninstall(),
		newCompletions(),
		newCron(),
		newUpgrade(),
	)
	return root
}

func newInit() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write ~/.config/pixiu.toml",
		RunE: func(cmd *cobra.Command, _ []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			path, err := configPath()
			if err != nil {
				return err
			}
			if _, err := os.Stat(path); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "config already exists: %s\n", path)
				return nil
			}
			if !yes {
				fmt.Fprintln(cmd.OutOrStdout(), "re-run with --yes to write the default config")
				return nil
			}
			return saveFile(defaultFile())
		},
	}
	cmd.Flags().Bool("yes", false, "write the default config without prompting")
	return cmd
}

func newUpdate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Refresh the remote Mihomo config, core, geodata, or UI",
		RunE: func(cmd *cobra.Command, _ []string) error {
			all, _ := cmd.Flags().GetBool("all")
			config, _ := cmd.Flags().GetBool("config")
			core, _ := cmd.Flags().GetBool("core")
			geodata, _ := cmd.Flags().GetBool("geodata")
			ui, _ := cmd.Flags().GetBool("ui")
			if all {
				config, core, geodata, ui = true, true, true, true
			}
			if !config && !core && !geodata && !ui {
				config = true
			}
			file, err := loadFile()
			if err != nil {
				return err
			}
			if config {
				if file.Remote == "" {
					fmt.Fprintln(cmd.OutOrStdout(), "remote is empty; config left unchanged")
				} else if _, err := fetchRemote(file.Remote); err != nil {
					return err
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "remote config is reachable")
				}
			}
			if core {
				fmt.Fprintf(cmd.OutOrStdout(), "mihomo binary: %s\n", file.MihomoBinary)
			}
			if geodata {
				fmt.Fprintln(cmd.OutOrStdout(), "geodata is left to Mihomo")
			}
			if ui {
				fmt.Fprintln(cmd.OutOrStdout(), "external ui is left to Mihomo")
			}
			return nil
		},
	}
	cmd.Flags().Bool("config", false, "refresh the remote config")
	cmd.Flags().Bool("core", false, "note the Mihomo core path")
	cmd.Flags().Bool("geodata", false, "leave geodata to Mihomo")
	cmd.Flags().Bool("ui", false, "leave the external UI to Mihomo")
	cmd.Flags().Bool("all", false, "refresh every component")
	return cmd
}

func newApply() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Write the runtime Mihomo config",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := writeRuntime(cmd)
			return err
		},
	}
}

func newStart() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Apply the runtime config and start Mihomo",
		RunE: func(cmd *cobra.Command, _ []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			file, err := loadFile()
			if err != nil {
				return err
			}
			decision := Decide(scanCurrent(), file.WifiBlacklist, true, yes)
			if decision.Warning != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), decision.Warning)
			}
			if decision.Block {
				return nil
			}
			path, err := writeRuntime(cmd)
			if err != nil {
				return err
			}
			return startMihomo(file, filepath.Dir(path))
		},
	}
	cmd.Flags().Bool("yes", false, "connect even when the current Wi-Fi is on wifi_blacklist")
	return cmd
}

func newStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether Mihomo is running",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, err := readPID()
			if err != nil || !processAlive(pid) {
				fmt.Fprintln(cmd.OutOrStdout(), "stopped")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "running %d\n", pid)
			return nil
		},
	}
}

func newStop() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop Mihomo",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return stopMihomo(cmd)
		},
	}
}

func newRestart() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Stop Mihomo and start it again",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := stopMihomo(cmd); err != nil {
				return err
			}
			return newStart().RunE(cmd, args)
		},
	}
	cmd.Flags().Bool("yes", false, "connect even when the current Wi-Fi is on wifi_blacklist")
	return cmd
}

func newLog() *cobra.Command {
	return &cobra.Command{
		Use:   "log",
		Short: "Print the Mihomo log",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := runtimeDir()
			if err != nil {
				return err
			}
			body, err := os.ReadFile(filepath.Join(dir, "mihomo.log"))
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintln(cmd.OutOrStdout(), "no log yet")
					return nil
				}
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), string(body))
			return nil
		},
	}
}

func newProxy() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Print shell snippets for the local Mihomo proxy",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "export",
			Short: "Print proxy environment exports",
			RunE: func(cmd *cobra.Command, _ []string) error {
				file, err := loadFile()
				if err != nil {
					return err
				}
				url := "http://" + file.ExternalController
				fmt.Fprintf(cmd.OutOrStdout(), "export http_proxy=%s\nexport https_proxy=%s\nexport all_proxy=socks5://127.0.0.1:7891\n", url, url)
				return nil
			},
		},
		&cobra.Command{
			Use:   "unset",
			Short: "Print commands that clear the proxy environment",
			RunE: func(cmd *cobra.Command, _ []string) error {
				fmt.Fprintln(cmd.OutOrStdout(), "unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY")
				return nil
			},
		},
	)
	return cmd
}

func newUninstall() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Pixiu config and runtime directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			path, err := configPath()
			if err != nil {
				return err
			}
			dir, err := runtimeDir()
			if err != nil {
				return err
			}
			if !yes {
				fmt.Fprintf(cmd.OutOrStdout(), "would remove %s and %s; re-run with --yes\n", path, dir)
				return nil
			}
			_ = os.Remove(path)
			return os.RemoveAll(dir)
		},
	}
	cmd.Flags().Bool("yes", false, "delete the config without prompting")
	return cmd
}

func newCompletions() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "completions [bash|fish|zsh]",
		Short:     "Print a shell completion script",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "fish", "zsh"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unsupported shell %s", args[0])
			}
		},
	}
	return cmd
}

func newCron() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage a periodic pixiu update",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "enable",
			Short: "Record a cron entry that runs pixiu update",
			RunE: func(cmd *cobra.Command, _ []string) error {
				dir, err := runtimeDir()
				if err != nil {
					return err
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
				line := "17 * * * * pixiu update --all\n"
				if err := os.WriteFile(filepath.Join(dir, "cron"), []byte(line), 0o644); err != nil {
					return err
				}
				fmt.Fprint(cmd.OutOrStdout(), line)
				return nil
			},
		},
		&cobra.Command{
			Use:   "disable",
			Short: "Remove the recorded cron entry",
			RunE: func(cmd *cobra.Command, _ []string) error {
				dir, err := runtimeDir()
				if err != nil {
					return err
				}
				_ = os.Remove(filepath.Join(dir, "cron"))
				fmt.Fprintln(cmd.OutOrStdout(), "disabled")
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show whether a cron entry is recorded",
			RunE: func(cmd *cobra.Command, _ []string) error {
				dir, err := runtimeDir()
				if err != nil {
					return err
				}
				body, err := os.ReadFile(filepath.Join(dir, "cron"))
				if err != nil {
					fmt.Fprintln(cmd.OutOrStdout(), "disabled")
					return nil
				}
				fmt.Fprint(cmd.OutOrStdout(), string(body))
				return nil
			},
		},
	)
	return cmd
}

func newUpgrade() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Report the installed pixiu version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			check, _ := cmd.Flags().GetBool("check")
			fmt.Fprintf(cmd.OutOrStdout(), "pixiu %s\n", version)
			if check {
				fmt.Fprintln(cmd.OutOrStdout(), "no newer release is configured")
			}
			return nil
		},
	}
	cmd.Flags().Bool("check", false, "check for a newer release")
	return cmd
}

func writeRuntime(cmd *cobra.Command) (string, error) {
	file, err := loadFile()
	if err != nil {
		return "", err
	}
	base := defaultRuntime(file)
	if file.Remote != "" {
		if body, err := fetchRemote(file.Remote); err == nil && len(bytes.TrimSpace(body)) > 0 {
			base = body
		}
	}
	info, err := queryNetBird()
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), ErrDaemonDown.Error())
	} else if merged, merr := MergeRuntime(base, info); merr == nil {
		base = merged
	}
	dir, err := runtimeDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, base, 0o644); err != nil {
		return "", err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
	return path, nil
}

func defaultRuntime(file File) []byte {
	controller := file.ExternalController
	if controller == "" {
		controller = "127.0.0.1:9090"
	}
	return []byte(fmt.Sprintf("mixed-port: 7890\nmode: rule\nexternal-controller: %s\nrules:\n  - MATCH,DIRECT\n", controller))
}

func fetchRemote(rawURL string) ([]byte, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remote config returned %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func startMihomo(file File, dir string) error {
	if pid, err := readPID(); err == nil && processAlive(pid) {
		return nil
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "mihomo.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	binary := file.MihomoBinary
	if binary == "" {
		binary = "mihomo"
	}
	cmd := exec.Command(binary, "-d", dir)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	_ = os.WriteFile(filepath.Join(dir, "mihomo.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	go func() {
		_ = cmd.Wait()
		logFile.Close()
	}()
	return nil
}

func stopMihomo(cmd *cobra.Command) error {
	pid, err := readPID()
	if err != nil {
		fmt.Fprintln(cmd.OutOrStdout(), "stopped")
		return nil
	}
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Kill()
	}
	dir, err := runtimeDir()
	if err == nil {
		_ = os.Remove(filepath.Join(dir, "mihomo.pid"))
	}
	fmt.Fprintln(cmd.OutOrStdout(), "stopped")
	return nil
}

func readPID() (int, error) {
	dir, err := runtimeDir()
	if err != nil {
		return 0, err
	}
	body, err := os.ReadFile(filepath.Join(dir, "mihomo.pid"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(body)))
}
