// Command dynsetup creates the DynamoDB tables mockwave uses, against a local
// DynamoDB (DynamoDB Local on http://localhost:8000).
//
// Run with dummy credentials; DynamoDB Local does not validate them:
//
//	AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local go run ./tools/dynsetup
//
// Existing tables are skipped, so re-running is safe.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const endpoint = "http://localhost:8000"

// table describes one table to create. Most mockwave tables are keyed by a
// single string "id"; matched requests are the exception and need a sort key.
type table struct {
	name string
	pk   string
	sk   string // empty for tables with no sort key
}

// tables mirrors the defaults in internal/adapters/out/dynamodb.Config. Keep
// this list in sync when a new table is added there.
var tables = []table{
	{name: "mockwave-rules", pk: "id"},
	{name: "mockwave-simulations", pk: "id"},
	{name: "mockwave-fault-profiles", pk: "id"},
	{name: "mockwave-scenarios", pk: "id"},
	{name: "mockwave-event-rules", pk: "id"},
	// Matched capture stores many rows per rule and range-scans them by sort
	// key prefix, so it is partitioned by rule_id with sk as the sort key.
	{name: "mockwave-matched-requests", pk: "rule_id", sk: "sk"},
}

func (t table) input() *dynamodb.CreateTableInput {
	attrs := []types.AttributeDefinition{
		{AttributeName: aws.String(t.pk), AttributeType: types.ScalarAttributeTypeS},
	}
	keys := []types.KeySchemaElement{
		{AttributeName: aws.String(t.pk), KeyType: types.KeyTypeHash},
	}
	if t.sk != "" {
		attrs = append(attrs, types.AttributeDefinition{
			AttributeName: aws.String(t.sk), AttributeType: types.ScalarAttributeTypeS,
		})
		keys = append(keys, types.KeySchemaElement{
			AttributeName: aws.String(t.sk), KeyType: types.KeyTypeRange,
		})
	}
	return &dynamodb.CreateTableInput{
		TableName:            aws.String(t.name),
		AttributeDefinitions: attrs,
		KeySchema:            keys,
		BillingMode:          types.BillingModePayPerRequest,
	}
}

func main() {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "load aws config:", err)
		os.Exit(1)
	}
	c := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	for _, t := range tables {
		key := t.pk
		if t.sk != "" {
			key += "/" + t.sk
		}
		if _, err := c.CreateTable(context.Background(), t.input()); err != nil {
			fmt.Fprintf(os.Stderr, "skip %s (exists?)\n", t.name)
			continue
		}
		fmt.Printf("created %s (%s)\n", t.name, key)
	}
}
