package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	grpcadapter "github.com/mockwave/mockwave/internal/adapters/in/grpc"
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
	var mockPort, adminPort, grpcPort int
	var protocolsStr, grpcProto string

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

			protocols := splitProtocols(protocolsStr)

			if containsProtocol(protocols, "grpc") {
				var registry *grpcadapter.FileRegistry
				if grpcProto != "" {
					registry, err = grpcadapter.LoadDescriptor(grpcProto)
					if err != nil {
						return fmt.Errorf("load grpc proto descriptor: %w", err)
					}
				}
				grpcSrv := srv.GRPCServer(registry)
				lis, err := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort))
				if err != nil {
					return fmt.Errorf("grpc listen: %w", err)
				}
				go func() {
					log.Printf("gRPC server listening on :%d", grpcPort)
					if err := grpcSrv.Serve(lis); err != nil {
						log.Fatalf("grpc server: %v", err)
					}
				}()
			}

			log.Printf("mock server listening on :%d (protocols: %s)", mockPort, protocolsStr)
			return http.ListenAndServe(fmt.Sprintf(":%d", mockPort), srv.MockHandler(protocols))
		},
	}
	cmd.Flags().StringVarP(&configFile, "config", "f", "", "path to JSON config file (required)")
	cmd.Flags().IntVar(&mockPort, "port", 8080, "mock server port")
	cmd.Flags().IntVar(&adminPort, "admin-port", 9090, "admin API port")
	cmd.Flags().StringVar(&protocolsStr, "protocols", "http", "comma-separated: http,graphql,soap,grpc")
	cmd.Flags().IntVar(&grpcPort, "grpc-port", 50051, "gRPC server port")
	cmd.Flags().StringVar(&grpcProto, "grpc-proto", "", "path to compiled .pb descriptor for gRPC proto conversion")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

func splitProtocols(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containsProtocol(protocols []string, target string) bool {
	for _, p := range protocols {
		if p == target {
			return true
		}
	}
	return false
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
