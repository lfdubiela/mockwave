package dynamostore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/mockwave/mockwave/domain"
)

// GetEventRules returns all AWS event rules. A missing table is treated as "no
// event rules configured" (nil, nil) so deployments that don't use event
// interception start cleanly.
func (s *Store) GetEventRules() ([]domain.EventRule, error) {
	out, err := s.client.Scan(context.Background(), &dynamodb.ScanInput{
		TableName: aws.String(s.eventRulesTable),
	})
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("dynamodb: scan event rules: %w", err)
	}
	rules := make([]domain.EventRule, 0, len(out.Items))
	for _, item := range out.Items {
		dataAttr, ok := item["data"].(*types.AttributeValueMemberS)
		if !ok {
			continue
		}
		var r domain.EventRule
		if err := json.Unmarshal([]byte(dataAttr.Value), &r); err != nil {
			return nil, fmt.Errorf("dynamodb: unmarshal event rule: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// SaveEventRule upserts an event rule by id and bumps the config version.
func (s *Store) SaveEventRule(r domain.EventRule) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("dynamodb: marshal event rule: %w", err)
	}
	_, err = s.client.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(s.eventRulesTable),
		Item: map[string]types.AttributeValue{
			"id":   &types.AttributeValueMemberS{Value: r.ID},
			"data": &types.AttributeValueMemberS{Value: string(data)},
		},
	})
	if err := wrapErr(err, "put event rule %q", r.ID); err != nil {
		return err
	}
	return s.bumpVersion()
}

// DeleteEventRule removes an event rule by id and bumps the config version.
func (s *Store) DeleteEventRule(id string) error {
	_, err := s.client.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
		TableName: aws.String(s.eventRulesTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
	})
	if err := wrapErr(err, "delete event rule %q", id); err != nil {
		return err
	}
	return s.bumpVersion()
}
