// Package scenarios provides scenario management services
package scenarios

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/estara-ai/www/internal/config"
	db "github.com/estara-ai/www/internal/db"
	"github.com/estara-ai/www/internal/db/queries"
)

var (
	ErrScenarioNotFound = errors.New("scenario not found")
	ErrUnauthorized     = errors.New("unauthorized to access scenario")
)

// Service handles scenario operations
type Service struct {
	store  *db.Store
	cfg    *config.Config
	logger *slog.Logger
}

// NewService creates a new scenarios service
func NewService(store *db.Store, cfg *config.Config) *Service {
	return &Service{
		store:  store,
		cfg:    cfg,
		logger: slog.Default().With("component", "scenarios_service"),
	}
}

// Scenario represents a user's investment scenario
type Scenario struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Description     *string                `json:"description"`
	Parameters      map[string]interface{} `json:"parameters"`
	Results         map[string]interface{} `json:"results,omitempty"`
	Tags            []string               `json:"tags"`
	Favorite        bool                   `json:"favorite"`
	HasAIParameters bool                   `json:"hasAIParameters"`
	CreatedAt       string                 `json:"createdAt"`
	LastModified    string                 `json:"lastModified"`
}

// CreateScenarioInput represents input for creating a scenario
type CreateScenarioInput struct {
	Name            string                 `json:"name"`
	Description     *string                `json:"description"`
	Parameters      map[string]interface{} `json:"parameters"`
	Results         map[string]interface{} `json:"results,omitempty"`
	Tags            []string               `json:"tags"`
	HasAIParameters bool                   `json:"hasAIParameters"`
}

// UpdateScenarioInput represents input for updating a scenario
type UpdateScenarioInput struct {
	Name            *string                `json:"name,omitempty"`
	Description     *string                `json:"description,omitempty"`
	Parameters      map[string]interface{} `json:"parameters,omitempty"`
	Results         map[string]interface{} `json:"results,omitempty"`
	Tags            []string               `json:"tags,omitempty"`
	Favorite        *bool                  `json:"favorite,omitempty"`
	HasAIParameters *bool                  `json:"hasAIParameters,omitempty"`
}

// List returns all scenarios for a user
func (s *Service) List(ctx context.Context, userID string) ([]Scenario, error) {
	rows, err := s.store.Q().ListUserScenarios(ctx, queries.ListUserScenariosParams{
		UserId: userID,
		Limit:  1000,
		Offset: 0,
	})
	if err != nil {
		return nil, err
	}

	scenarios := make([]Scenario, 0, len(rows))
	for _, row := range rows {
		scenarios = append(scenarios, dbScenarioToScenario(row))
	}

	return scenarios, nil
}

// Get returns a specific scenario by ID, verifying user ownership
func (s *Service) Get(ctx context.Context, userID, scenarioID string) (*Scenario, error) {
	row, err := s.store.Q().GetScenarioByIDAndUser(ctx, queries.GetScenarioByIDAndUserParams{
		ID:     scenarioID,
		UserId: userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScenarioNotFound
		}
		return nil, err
	}

	sc := dbScenarioToScenario(row)
	return &sc, nil
}

// Create creates a new scenario for a user
func (s *Service) Create(ctx context.Context, userID string, input CreateScenarioInput) (*Scenario, error) {
	// Generate ID
	idBytes := make([]byte, 12)
	_, _ = rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)

	// Prepare tags
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}

	// Marshal parameters, results, and tags to JSON
	paramsJSON, err := json.Marshal(input.Parameters)
	if err != nil {
		return nil, err
	}

	var resultsJSON []byte
	if input.Results != nil {
		resultsJSON, err = json.Marshal(input.Results)
		if err != nil {
			return nil, err
		}
	}

	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}

	row, err := s.store.Q().CreateScenario(ctx, queries.CreateScenarioParams{
		ID:     id,
		UserId: userID,
		Name:   input.Name,
		Description: pgtype.Text{
			String: ptrToString(input.Description),
			Valid:  input.Description != nil,
		},
		Parameters:      paramsJSON,
		Results:         resultsJSON,
		Tags:            tagsJSON,
		Favorite:        false,
		HasAIParameters: input.HasAIParameters,
	})
	if err != nil {
		return nil, err
	}

	sc := dbScenarioToScenario(row)
	return &sc, nil
}

// Update updates an existing scenario
func (s *Service) Update(ctx context.Context, userID, scenarioID string, input UpdateScenarioInput) (*Scenario, error) {
	// First verify ownership
	_, err := s.store.Q().GetScenarioByIDAndUser(ctx, queries.GetScenarioByIDAndUserParams{
		ID:     scenarioID,
		UserId: userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScenarioNotFound
		}
		return nil, err
	}

	// Marshal optional fields to JSON
	var paramsJSON json.RawMessage
	if input.Parameters != nil {
		paramsJSON, err = json.Marshal(input.Parameters)
		if err != nil {
			return nil, err
		}
	}

	var resultsJSON []byte
	if input.Results != nil {
		resultsJSON, err = json.Marshal(input.Results)
		if err != nil {
			return nil, err
		}
	}

	var tagsJSON json.RawMessage
	if input.Tags != nil {
		tagsJSON, err = json.Marshal(input.Tags)
		if err != nil {
			return nil, err
		}
	}

	// Prepare name - use empty string if nil (COALESCE in SQL handles this)
	name := ""
	if input.Name != nil {
		name = *input.Name
	}

	// Prepare description
	desc := pgtype.Text{Valid: false}
	if input.Description != nil {
		desc = pgtype.Text{String: *input.Description, Valid: true}
	}

	// Prepare favorite and hasAIParameters
	favorite := false
	if input.Favorite != nil {
		favorite = *input.Favorite
	}

	hasAIParams := false
	if input.HasAIParameters != nil {
		hasAIParams = *input.HasAIParameters
	}

	row, err := s.store.Q().UpdateScenario(ctx, queries.UpdateScenarioParams{
		ID:              scenarioID,
		Name:            name,
		Description:     desc,
		Parameters:      paramsJSON,
		Results:         resultsJSON,
		Tags:            tagsJSON,
		Favorite:        favorite,
		HasAIParameters: hasAIParams,
	})
	if err != nil {
		return nil, err
	}

	sc := dbScenarioToScenario(row)
	return &sc, nil
}

// Delete deletes a scenario
func (s *Service) Delete(ctx context.Context, userID, scenarioID string) error {
	// First verify ownership
	_, err := s.store.Q().GetScenarioByIDAndUser(ctx, queries.GetScenarioByIDAndUserParams{
		ID:     scenarioID,
		UserId: userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrScenarioNotFound
		}
		return err
	}

	return s.store.Q().DeleteScenarioByIDAndUser(ctx, queries.DeleteScenarioByIDAndUserParams{
		ID:     scenarioID,
		UserId: userID,
	})
}

// dbScenarioToScenario converts a sqlc-generated Scenario to the API Scenario type.
func dbScenarioToScenario(row queries.Scenario) Scenario {
	// Convert description
	var desc *string
	if row.Description.Valid {
		desc = &row.Description.String
	}

	// Convert parameters
	var params map[string]interface{}
	if len(row.Parameters) > 0 {
		_ = json.Unmarshal(row.Parameters, &params)
	}
	if params == nil {
		params = map[string]interface{}{}
	}

	// Convert results
	var results map[string]interface{}
	if len(row.Results) > 0 {
		_ = json.Unmarshal(row.Results, &results)
	}

	// Convert tags
	var tags []string
	if len(row.Tags) > 0 {
		_ = json.Unmarshal(row.Tags, &tags)
	}
	if tags == nil {
		tags = []string{}
	}

	// Convert timestamps
	createdAt := ""
	if row.CreatedAt.Valid {
		createdAt = row.CreatedAt.Time.Format(time.RFC3339)
	}
	lastModified := ""
	if row.LastModified.Valid {
		lastModified = row.LastModified.Time.Format(time.RFC3339)
	}

	return Scenario{
		ID:              row.ID,
		Name:            row.Name,
		Description:     desc,
		Parameters:      params,
		Results:         results,
		Tags:            tags,
		Favorite:        row.Favorite,
		HasAIParameters: row.HasAIParameters,
		CreatedAt:       createdAt,
		LastModified:    lastModified,
	}
}

// ptrToString safely dereferences a string pointer, returning empty string if nil.
func ptrToString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
