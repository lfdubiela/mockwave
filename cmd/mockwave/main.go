package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/server"
	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{Use: "mockwave", Short: "Multi-protocol mock server"}
	root.AddCommand(startCmd(), validateCmd(), versionCmd())
	return root
}

func startCmd() *cobra.Command {
	var configFile string
	var mockPort, adminPort int
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the mock server",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := jsonfile.NewStore(configFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			srv, err := server.New(server.Config{MockPort: mockPort, AdminPort: adminPort, Store: store})
			if err != nil {
				return err
			}
			adminMux := restapi.NewMux(store, func() {
				if err := srv.Rebuild(); err != nil {
					log.Printf("hot-reload failed: %v", err)
				}
			})
			go func() {
				log.Printf("admin API listening on :%d", adminPort)
				if err := http.ListenAndServe(fmt.Sprintf(":%d", adminPort), adminMux); err != nil {
					log.Fatalf("admin server: %v", err)
				}
			}()
			log.Printf("mock server listening on :%d", mockPort)
			return http.ListenAndServe(fmt.Sprintf(":%d", mockPort), srv.HTTPHandler())
		},
	}
	cmd.Flags().StringVarP(&configFile, "config", "f", "", "path to JSON config file (required)")
	cmd.Flags().IntVar(&mockPort, "port", 8080, "mock server port")
	cmd.Flags().IntVar(&adminPort, "admin-port", 9090, "admin API port")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate a config file without starting the server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			var cfg struct {
				Rules []interface{} `json:"rules"`
			}
			if err := json.NewDecoder(f).Decode(&cfg); err != nil {
				return fmt.Errorf("invalid JSON: %w", err)
			}
			fmt.Printf("config valid: %d rules found\n", len(cfg.Rules))
			return nil
		},
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(cmd *cobra.Command, args []string) { fmt.Println("mockwave v0.1.0") },
	}
}
