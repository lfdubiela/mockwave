package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	grpcadapter "github.com/mockwave/mockwave/internal/adapters/in/grpc"
	cosmos "github.com/mockwave/mockwave/internal/adapters/out/cosmos"
	dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	mongodb "github.com/mockwave/mockwave/internal/adapters/out/mongodb"
	"github.com/mockwave/mockwave/server"
	"github.com/mockwave/mockwave/store"
	"github.com/spf13/cobra"
	googlegrpc "google.golang.org/grpc"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{Use: "mockwave", Short: "Multi-protocol mock server"}
	root.AddCommand(startCmd(), validateCmd(), versionCmd(), mcpCmd(), faultCmd(), chaosCmd(), scenarioCmd())
	return root
}

func startCmd() *cobra.Command {
	var (
		configFile   string
		mockPort     int
		adminPort    int
		protocolsStr   string
		grpcPort       int
		grpcProto      string
		storeType      string
		reloadInterval time.Duration
		opts           storeOpts

		matchedCapture      bool
		matchedTTL          int
		matchedBufferSize   int
		matchedSyncInterval int

		eventCapture      bool
		eventTTL          int
		eventBufferSize   int
		eventSyncInterval int
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the mock server",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := buildStore(storeType, configFile, opts)
			if err != nil {
				return fmt.Errorf("init store: %w", err)
			}
			srv, err := server.New(server.Config{
				MockPort:       mockPort,
				AdminPort:      adminPort,
				Store:          s,
				ReloadInterval: reloadInterval,
				ImportExport:   storeType != "json",
				Matched: server.MatchedConfig{
					Enabled:      matchedCapture,
					TTL:          time.Duration(matchedTTL) * time.Second,
					BufferSize:   matchedBufferSize,
					SyncInterval: time.Duration(matchedSyncInterval) * time.Second,
				},
				Event: server.EventConfig{
					Enabled:      eventCapture,
					TTL:          time.Duration(eventTTL) * time.Second,
					BufferSize:   eventBufferSize,
					SyncInterval: time.Duration(eventSyncInterval) * time.Second,
				},
			})
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			proxy := srv.NewProxy() // metrics-wrapped; feeds admin dashboard automatically
			protocols := splitProtocols(protocolsStr)

			var grpcSrv *googlegrpc.Server
			if containsProtocol(protocols, "grpc") {
				var registry *grpcadapter.FileRegistry
				if grpcProto != "" {
					registry, err = grpcadapter.LoadDescriptor(grpcProto)
					if err != nil {
						return fmt.Errorf("load grpc proto descriptor: %w", err)
					}
				}
				grpcSrv = srv.GRPCServer(registry, proxy)
				lis, err := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort))
				if err != nil {
					return fmt.Errorf("grpc listen: %w", err)
				}
				go func() {
					log.Printf("gRPC server listening on :%d", grpcPort)
					if err := grpcSrv.Serve(lis); err != nil {
						log.Printf("grpc server stopped: %v", err)
					}
				}()
			}

			mockSrv := &http.Server{
				Addr:    fmt.Sprintf(":%d", mockPort),
				Handler: srv.MockHandler(protocols, proxy),
			}
			go func() {
				log.Printf("mock server listening on :%d (protocols: %s, store: %s)", mockPort, protocolsStr, storeType)
				log.Printf("admin API listening on :%d", adminPort)
				if err := mockSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("mock server stopped: %v", err)
				}
			}()

			<-ctx.Done()
			log.Println("shutting down...")

			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if grpcSrv != nil {
				grpcSrv.GracefulStop()
			}
			if err := mockSrv.Shutdown(shutCtx); err != nil {
				log.Printf("mock server shutdown: %v", err)
			}
			return srv.Shutdown(shutCtx)
		},
	}

	cmd.Flags().StringVarP(&configFile, "config", "f", "", "path to JSON config file (required for --store=json)")
	cmd.Flags().IntVar(&mockPort, "port", 8080, "mock server port")
	cmd.Flags().IntVar(&adminPort, "admin-port", 9090, "admin API port")
	cmd.Flags().StringVar(&protocolsStr, "protocols", "http", "comma-separated: http,graphql,soap,grpc,aws")
	cmd.Flags().IntVar(&grpcPort, "grpc-port", 50051, "gRPC server port")
	cmd.Flags().StringVar(&grpcProto, "grpc-proto", "", "path to compiled .pb descriptor for gRPC proto conversion")

	// Store selection
	cmd.Flags().StringVar(&storeType, "store", "json", "storage backend: json|dynamodb|mongo|cosmos")
	cmd.Flags().DurationVar(&reloadInterval, "reload-interval", 15*time.Second, "version-poll reload interval for remote stores")

	// DynamoDB flags
	cmd.Flags().StringVar(&opts.DynamoRulesTable, "dynamo-rules-table", "mockwave-rules", "DynamoDB table for rules")
	cmd.Flags().StringVar(&opts.DynamoSimsTable, "dynamo-sims-table", "mockwave-simulations", "DynamoDB table for simulations")
	cmd.Flags().StringVar(&opts.DynamoFaultsTable, "dynamo-faults-table", "mockwave-fault-profiles", "DynamoDB table for fault profiles")
	cmd.Flags().StringVar(&opts.DynamoScenariosTable, "dynamo-scenarios-table", "mockwave-scenarios", "DynamoDB table for scenarios")
	cmd.Flags().StringVar(&opts.DynamoEventRulesTable, "dynamo-event-rules-table", "mockwave-event-rules", "DynamoDB table for AWS event rules")
	cmd.Flags().StringVar(&opts.DynamoRegion, "dynamo-region", "us-east-1", "AWS region for DynamoDB")
	cmd.Flags().StringVar(&opts.DynamoEndpoint, "dynamo-endpoint", "", "custom DynamoDB endpoint (e.g. http://localhost:8000)")

	// MongoDB flags
	cmd.Flags().StringVar(&opts.MongoURI, "mongo-uri", "mongodb://localhost:27017", "MongoDB connection URI")
	cmd.Flags().StringVar(&opts.MongoDB, "mongo-db", "mockwave", "MongoDB database name")

	// Cosmos DB flags
	cmd.Flags().StringVar(&opts.CosmosURI, "cosmos-uri", "", "Cosmos DB connection string (MongoDB API)")
	cmd.Flags().StringVar(&opts.CosmosDB, "cosmos-db", "mockwave", "Cosmos DB database name")

	// Matched request capture flags
	cmd.Flags().BoolVar(&matchedCapture, "matched-capture", false, "enable matched request capture")
	cmd.Flags().IntVar(&matchedTTL, "matched-ttl", 3600, "TTL in seconds for captured matched requests")
	cmd.Flags().IntVar(&matchedBufferSize, "matched-buffer-size", 10000, "in-memory buffer size for matched request capture")
	cmd.Flags().IntVar(&matchedSyncInterval, "matched-sync-interval", 30, "sync interval in seconds for matched request capture")

	cmd.Flags().BoolVar(&eventCapture, "event-capture", false, "enable AWS event interception + capture (use with --protocols aws)")
	cmd.Flags().IntVar(&eventTTL, "event-ttl", 3600, "TTL in seconds for captured events")
	cmd.Flags().IntVar(&eventBufferSize, "event-buffer-size", 10000, "in-memory buffer size for event capture")
	cmd.Flags().IntVar(&eventSyncInterval, "event-sync-interval", 30, "sync interval in seconds for event capture")

	return cmd
}

// storeOpts holds connection details for non-JSON store backends.
type storeOpts struct {
	// DynamoDB
	DynamoRulesTable      string
	DynamoSimsTable       string
	DynamoFaultsTable     string
	DynamoScenariosTable  string
	DynamoEventRulesTable string
	DynamoRegion          string
	DynamoEndpoint        string // optional; empty = use AWS default endpoint

	// MongoDB
	MongoURI string
	MongoDB  string

	// Cosmos DB
	CosmosURI string
	CosmosDB  string
}

// buildStore constructs the DataStore for the chosen backend.
func buildStore(storeType, configFile string, opts storeOpts) (store.DataStore, error) {
	switch storeType {
	case "json":
		if configFile == "" {
			return nil, fmt.Errorf("--config is required when --store=json")
		}
		return jsonfile.NewStore(configFile)
	case "dynamodb":
		return dynamostore.NewStore(dynamostore.Config{
			RulesTable:      opts.DynamoRulesTable,
			SimsTable:       opts.DynamoSimsTable,
			FaultsTable:     opts.DynamoFaultsTable,
			ScenariosTable:  opts.DynamoScenariosTable,
			EventRulesTable: opts.DynamoEventRulesTable,
			Region:          opts.DynamoRegion,
			Endpoint:        opts.DynamoEndpoint,
		})
	case "mongo":
		return mongodb.NewStore(opts.MongoURI, opts.MongoDB)
	case "cosmos":
		return cosmos.NewStore(opts.CosmosURI, opts.CosmosDB)
	default:
		return nil, fmt.Errorf("unknown store %q — valid: json, dynamodb, mongo, cosmos", storeType)
	}
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

// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(cmd *cobra.Command, args []string) { fmt.Println("mockwave " + version) },
	}
}
