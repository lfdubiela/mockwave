package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

// adminDo issues a request against the mockwave admin API.
func adminDo(base, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

// checkStatus returns an error including status code and body when the
// response status differs from want.
func checkStatus(resp *http.Response, want int) error {
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("admin API returned %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	return nil
}

func faultCmd() *cobra.Command {
	var adminURL string
	cmd := &cobra.Command{Use: "fault", Short: "Manage chaos fault profiles"}
	cmd.PersistentFlags().StringVar(&adminURL, "admin-url", "http://localhost:9090", "mockwave admin API base URL")

	list := &cobra.Command{
		Use:   "list",
		Short: "List fault profiles",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFaultList(adminURL, cmd.OutOrStdout())
		},
	}
	get := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one fault profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFaultGet(adminURL, args[0], cmd.OutOrStdout())
		},
	}
	var file string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a fault profile from a JSON file",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFaultCreate(adminURL, file, cmd.OutOrStdout())
		},
	}
	create.Flags().StringVarP(&file, "file", "f", "", "path to fault profile JSON")
	_ = create.MarkFlagRequired("file")
	del := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a fault profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runFaultDelete(adminURL, args[0])
		},
	}
	cmd.AddCommand(list, get, create, del)
	return cmd
}

func runFaultList(base string, out io.Writer) error {
	resp, err := adminDo(base, http.MethodGet, "/api/faults", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return err
	}
	_, err = io.Copy(out, resp.Body)
	return err
}

func runFaultGet(base, id string, out io.Writer) error {
	resp, err := adminDo(base, http.MethodGet, "/api/faults/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return err
	}
	_, err = io.Copy(out, resp.Body)
	return err
}

func runFaultCreate(base, file string, out io.Writer) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if !json.Valid(data) {
		return fmt.Errorf("%s is not valid JSON", file)
	}
	resp, err := adminDo(base, http.MethodPost, "/api/faults", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusCreated); err != nil {
		return err
	}
	fmt.Fprintln(out, "created")
	return nil
}

func runFaultDelete(base, id string) error {
	resp, err := adminDo(base, http.MethodDelete, "/api/faults/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp, http.StatusNoContent)
}

func chaosCmd() *cobra.Command {
	var adminURL string
	cmd := &cobra.Command{Use: "chaos", Short: "Control the global chaos kill switch"}
	cmd.PersistentFlags().StringVar(&adminURL, "admin-url", "http://localhost:9090", "mockwave admin API base URL")

	post := func(use, short, path string) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			RunE: func(*cobra.Command, []string) error {
				return runChaosPost(adminURL, path)
			},
		}
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show chaos status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChaosStatus(adminURL, cmd.OutOrStdout())
		},
	}
	cmd.AddCommand(
		post("halt", "Disable all fault injection", "/api/chaos/halt"),
		post("resume", "Re-enable fault injection", "/api/chaos/resume"),
		status,
	)
	return cmd
}

func runChaosPost(base, path string) error {
	resp, err := adminDo(base, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp, http.StatusNoContent)
}

func runChaosStatus(base string, out io.Writer) error {
	resp, err := adminDo(base, http.MethodGet, "/api/chaos/status", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return err
	}
	_, err = io.Copy(out, resp.Body)
	return err
}
