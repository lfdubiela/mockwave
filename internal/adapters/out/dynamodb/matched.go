package dynamostore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
)

// compile-time MatchedStore check.
var _ store.MatchedStore = (*Store)(nil)

// SK prefixes:
//
//	r#<id>      – request item
//	br#<bodyID> – request body item
//	bs#<bodyID> – response body item
const (
	skReqPrefix = "r#"
	skReqBody   = "br#"
	skRespBody  = "bs#"
)

// DynamoQueryClient extends DynamoClient with Query support (needed for ListMatched).
// We declare a local interface and assert Store's real client satisfies it at runtime.
type dynamoQuerier interface {
	DynamoClient
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	BatchWriteItem(ctx context.Context, params *dynamodb.BatchWriteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
}

// SaveMatched upserts a batch of requests and their out-of-line bodies.
func (s *Store) SaveMatched(ctx context.Context, reqs []matched.Request, reqBodies []matched.RequestBody, respBodies []matched.ResponseBody) error {
	table := s.matchedTable_
	for _, r := range reqs {
		data, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("dynamodb: marshal matched request: %w", err)
		}
		item := map[string]types.AttributeValue{
			"rule_id": &types.AttributeValueMemberS{Value: r.RuleID},
			"sk":      &types.AttributeValueMemberS{Value: skReqPrefix + r.ID},
			"id":      &types.AttributeValueMemberS{Value: r.ID},
			"data":    &types.AttributeValueMemberS{Value: string(data)},
		}
		if r.TTL > 0 {
			item["ttl"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(r.TTL, 10)}
		}
		_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(table),
			Item:      item,
		})
		if err != nil {
			return fmt.Errorf("dynamodb: put matched request %q: %w", r.ID, err)
		}
	}
	for _, rb := range reqBodies {
		bodyJSON, err := json.Marshal(rb.Body)
		if err != nil {
			return fmt.Errorf("dynamodb: marshal req body: %w", err)
		}
		item := map[string]types.AttributeValue{
			"rule_id": &types.AttributeValueMemberS{Value: "body"},
			"sk":      &types.AttributeValueMemberS{Value: skReqBody + rb.ID},
			"id":      &types.AttributeValueMemberS{Value: rb.ID},
			"data":    &types.AttributeValueMemberS{Value: string(bodyJSON)},
		}
		// Without this the row never expires: DynamoDB only reaps items that
		// carry the ttl attribute, so bodies outlived the requests owning them.
		if rb.TTL > 0 {
			item["ttl"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(rb.TTL, 10)}
		}
		_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(table),
			Item:      item,
		})
		if err != nil {
			return fmt.Errorf("dynamodb: put req body %q: %w", rb.ID, err)
		}
	}
	for _, sb := range respBodies {
		bodyJSON, err := json.Marshal(sb.Body)
		if err != nil {
			return fmt.Errorf("dynamodb: marshal resp body: %w", err)
		}
		item := map[string]types.AttributeValue{
			"rule_id": &types.AttributeValueMemberS{Value: "body"},
			"sk":      &types.AttributeValueMemberS{Value: skRespBody + sb.ID},
			"id":      &types.AttributeValueMemberS{Value: sb.ID},
			"data":    &types.AttributeValueMemberS{Value: string(bodyJSON)},
		}
		if sb.TTL > 0 {
			item["ttl"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(sb.TTL, 10)}
		}
		_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(table),
			Item:      item,
		})
		if err != nil {
			return fmt.Errorf("dynamodb: put resp body %q: %w", sb.ID, err)
		}
	}
	return nil
}

// ListMatched returns a filtered, paginated page for a rule, newest first.
// When ruleID == "" a Scan is used (hydration path).
func (s *Store) ListMatched(ctx context.Context, ruleID string, q store.MatchedQuery) (store.MatchedPage, error) {
	afterID, _ := matched.DecodeCursor(q.Cursor)
	limit := q.EffectiveLimit()
	table := s.matchedTable_

	var items []map[string]types.AttributeValue

	if ruleID == "" {
		// Scan across all partitions (hydration only).
		filterExpr := "begins_with(sk, :pfx)"
		exprVals := map[string]types.AttributeValue{
			":pfx": &types.AttributeValueMemberS{Value: skReqPrefix},
		}
		// Stop as soon as a full page is available. This previously read the
		// entire table into memory and applied the limit afterwards, so a
		// small page against a large table walked every row -- enough to hang
		// startup once the table reached seven figures.
		var lastKey map[string]types.AttributeValue
		matching := 0
		for {
			input := &dynamodb.ScanInput{
				TableName:                 aws.String(table),
				FilterExpression:          aws.String(filterExpr),
				ExpressionAttributeValues: exprVals,
			}
			if lastKey != nil {
				input.ExclusiveStartKey = lastKey
			}
			out, err := s.client.Scan(ctx, input)
			if err != nil {
				return store.MatchedPage{}, fmt.Errorf("dynamodb: scan matched requests: %w", err)
			}
			items = append(items, out.Items...)
			// Count only rows that survive decoding and the caller's filters,
			// mirroring what the page assembly below will keep.
			for _, it := range out.Items {
				r, err := decodeMatchedItem(it)
				if err != nil || !q.Matches(r) {
					continue
				}
				matching++
			}
			if matching >= limit {
				break
			}
			if len(out.LastEvaluatedKey) == 0 {
				break
			}
			lastKey = out.LastEvaluatedKey
		}
	} else {
		querier, ok := s.client.(dynamoQuerier)
		if !ok {
			return store.MatchedPage{}, fmt.Errorf("dynamodb: client does not support Query (needed for ListMatched with ruleID)")
		}
		input := &dynamodb.QueryInput{
			TableName:              aws.String(table),
			KeyConditionExpression: aws.String("rule_id = :rk AND begins_with(sk, :pfx)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":rk":  &types.AttributeValueMemberS{Value: ruleID},
				":pfx": &types.AttributeValueMemberS{Value: skReqPrefix},
			},
			ScanIndexForward: aws.Bool(false), // newest first (desc by SK)
		}
		if afterID != "" {
			input.ExclusiveStartKey = map[string]types.AttributeValue{
				"rule_id": &types.AttributeValueMemberS{Value: ruleID},
				"sk":      &types.AttributeValueMemberS{Value: skReqPrefix + afterID},
			}
		}
		// Fetch up to 10x limit to account for client-side filtering.
		fetchLimit := int32(limit * 10)
		input.Limit = aws.Int32(fetchLimit)
		out, err := querier.Query(ctx, input)
		if err != nil {
			return store.MatchedPage{}, fmt.Errorf("dynamodb: query matched requests: %w", err)
		}
		items = out.Items
	}

	// Decode and apply client-side filters.
	var result []matched.Request
	for _, item := range items {
		r, err := decodeMatchedItem(item)
		if err != nil {
			continue
		}
		if !q.Matches(r) {
			continue
		}
		result = append(result, r)
		if len(result) == limit {
			break
		}
	}

	page := store.MatchedPage{Items: result}
	if len(result) == limit {
		page.NextCursor = matched.EncodeCursor(result[len(result)-1].ID)
	}
	return page, nil
}

// GetMatched returns the full request (with bodies), or (nil, nil) when absent.
func (s *Store) GetMatched(ctx context.Context, ruleID, id string) (*matched.FullRequest, error) {
	table := s.matchedTable_
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"rule_id": &types.AttributeValueMemberS{Value: ruleID},
			"sk":      &types.AttributeValueMemberS{Value: skReqPrefix + id},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb: get matched request %q: %w", id, err)
	}
	if out.Item == nil {
		return nil, nil
	}
	r, err := decodeMatchedItem(out.Item)
	if err != nil {
		return nil, err
	}
	full := &matched.FullRequest{Request: r}
	if r.RequestBodyID != "" {
		body, err := s.getBody(ctx, table, skReqBody+r.RequestBodyID)
		if err == nil {
			_ = json.Unmarshal(body, &full.RequestBody)
		}
	}
	if r.ResponseBodyID != "" {
		body, err := s.getBody(ctx, table, skRespBody+r.ResponseBodyID)
		if err == nil {
			var v interface{}
			if jsonErr := json.Unmarshal(body, &v); jsonErr == nil {
				full.ResponseBody = v
			}
		}
	}
	return full, nil
}

func (s *Store) getBody(ctx context.Context, table, sk string) ([]byte, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"rule_id": &types.AttributeValueMemberS{Value: "body"},
			"sk":      &types.AttributeValueMemberS{Value: sk},
		},
	})
	if err != nil || out.Item == nil {
		return nil, fmt.Errorf("dynamodb: body not found: %s", sk)
	}
	dataAttr, ok := out.Item["data"].(*types.AttributeValueMemberS)
	if !ok {
		return nil, fmt.Errorf("dynamodb: body has no data attribute")
	}
	return []byte(dataAttr.Value), nil
}

// DeleteMatched removes a rule's captures, or all when ruleID == "".
func (s *Store) DeleteMatched(ctx context.Context, ruleID string) error {
	table := s.matchedTable_

	if ruleID == "" {
		// Delete everything by scanning all items and batch-deleting.
		return s.deleteAllMatched(ctx, table)
	}

	querier, ok := s.client.(dynamoQuerier)
	if !ok {
		// Fallback: scan and filter.
		return s.deleteMatchedByScan(ctx, table, ruleID)
	}
	// Query the partition for this ruleID.
	var lastKey map[string]types.AttributeValue
	for {
		input := &dynamodb.QueryInput{
			TableName:              aws.String(table),
			KeyConditionExpression: aws.String("rule_id = :rk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":rk": &types.AttributeValueMemberS{Value: ruleID},
			},
			ProjectionExpression: aws.String("rule_id, sk"),
		}
		if lastKey != nil {
			input.ExclusiveStartKey = lastKey
		}
		out, err := querier.Query(ctx, input)
		if err != nil {
			return fmt.Errorf("dynamodb: query for delete matched rule %q: %w", ruleID, err)
		}
		if err := s.batchDelete(ctx, table, out.Items); err != nil {
			return err
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		lastKey = out.LastEvaluatedKey
	}
	return nil
}

func (s *Store) deleteAllMatched(ctx context.Context, table string) error {
	var lastKey map[string]types.AttributeValue
	for {
		out, err := s.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:            aws.String(table),
			ProjectionExpression: aws.String("rule_id, sk"),
			ExclusiveStartKey:    lastKey,
		})
		if err != nil {
			return fmt.Errorf("dynamodb: scan for delete all matched: %w", err)
		}
		if err := s.batchDelete(ctx, table, out.Items); err != nil {
			return err
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		lastKey = out.LastEvaluatedKey
	}
	return nil
}

func (s *Store) deleteMatchedByScan(ctx context.Context, table, ruleID string) error {
	// Pages, like deleteAllMatched above. A filtered Scan is doubly prone to
	// paging: DynamoDB applies the 1MB cap before the filter, so even a small
	// result set can arrive split across pages, and some of those pages can be
	// empty while more data still remains.
	var lastKey map[string]types.AttributeValue
	for {
		out, err := s.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(table),
			FilterExpression:          aws.String("rule_id = :rk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":rk": &types.AttributeValueMemberS{Value: ruleID}},
			ProjectionExpression:      aws.String("rule_id, sk"),
			ExclusiveStartKey:         lastKey,
		})
		if err != nil {
			return fmt.Errorf("dynamodb: scan for delete matched rule %q: %w", ruleID, err)
		}
		if err := s.batchDelete(ctx, table, out.Items); err != nil {
			return err
		}
		// len, not nil: an empty-but-non-nil key would otherwise loop forever.
		if len(out.LastEvaluatedKey) == 0 {
			return nil
		}
		lastKey = out.LastEvaluatedKey
	}
}

func (s *Store) batchDelete(ctx context.Context, table string, items []map[string]types.AttributeValue) error {
	if len(items) == 0 {
		return nil
	}
	querier, ok := s.client.(dynamoQuerier)
	if !ok {
		// Fall back to individual DeleteItem calls.
		for _, item := range items {
			_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
				TableName: aws.String(table),
				Key: map[string]types.AttributeValue{
					"rule_id": item["rule_id"],
					"sk":      item["sk"],
				},
			})
			if err != nil {
				return fmt.Errorf("dynamodb: delete matched item: %w", err)
			}
		}
		return nil
	}

	// Use BatchWriteItem in chunks of 25 (DynamoDB limit).
	for len(items) > 0 {
		chunk := items
		if len(chunk) > 25 {
			chunk = items[:25]
			items = items[25:]
		} else {
			items = nil
		}
		writes := make([]types.WriteRequest, 0, len(chunk))
		for _, item := range chunk {
			writes = append(writes, types.WriteRequest{
				DeleteRequest: &types.DeleteRequest{
					Key: map[string]types.AttributeValue{
						"rule_id": item["rule_id"],
						"sk":      item["sk"],
					},
				},
			})
		}
		_, err := querier.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]types.WriteRequest{table: writes},
		})
		if err != nil {
			return fmt.Errorf("dynamodb: batch delete matched items: %w", err)
		}
	}
	return nil
}

// SweepExpired is a no-op — native TTL handles expiry.
func (s *Store) SweepExpired(_ context.Context, _ int64) (int, error) {
	return 0, nil
}

func decodeMatchedItem(item map[string]types.AttributeValue) (matched.Request, error) {
	dataAttr, ok := item["data"].(*types.AttributeValueMemberS)
	if !ok {
		return matched.Request{}, fmt.Errorf("dynamodb: matched item missing data attribute")
	}
	var r matched.Request
	if err := json.Unmarshal([]byte(dataAttr.Value), &r); err != nil {
		return matched.Request{}, fmt.Errorf("dynamodb: unmarshal matched request: %w", err)
	}
	return r, nil
}
