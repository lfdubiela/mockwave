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

func main() {
	cfg, _ := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"))
	c := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String("http://localhost:8000") })
	for _, name := range []string{"mockwave-rules", "mockwave-simulations", "mockwave-fault-profiles", "mockwave-scenarios"} {
		_, err := c.CreateTable(context.Background(), &dynamodb.CreateTableInput{
			TableName:            aws.String(name),
			AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: types.ScalarAttributeTypeS}},
			KeySchema:            []types.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: types.KeyTypeHash}},
			BillingMode:          types.BillingModePayPerRequest,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "skip", name, "(exists?)")
		} else {
			fmt.Println("created", name)
		}
	}
}
