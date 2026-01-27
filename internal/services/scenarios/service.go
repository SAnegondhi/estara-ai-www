// Package scenarios provides scenario management services
package scenarios

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
)

var (
	ErrScenarioNotFound = errors.New("scenario not found")
	ErrUnauthorized     = errors.New("unauthorized to access scenario")
)

// Service handles scenario operations
type Service struct {
	db     *postgres.DB
	cfg    *config.Config
	logger *slog.Logger
}

// NewService creates a new scenarios service
func NewService(db *postgres.DB, cfg *config.Config) *Service {
	return &Service{
		db:     db,
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
	query := `
		SELECT id, name, description, parameters, results, tags, favorite, "hasAIParameters", "createdAt", "lastModified"
		FROM scenarios
		WHERE "userId" = $1
		ORDER BY "lastModified" DESC
	`

	rows, err := s.db.Main.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scenarios []Scenario
	for rows.Next() {
		var sc dbScenario
		err := rows.Scan(
			&sc.ID,
			&sc.Name,
			&sc.Description,
			&sc.Parameters,
			&sc.Results,
			&sc.Tags,
			&sc.Favorite,
			&sc.HasAIParameters,
			&sc.CreatedAt,
			&sc.LastModified,
		)
		if err != nil {
			return nil, err
		}
		scenarios = append(scenarios, sc.toScenario())
	}

	if scenarios == nil {
		scenarios = []Scenario{}
	}

	return scenarios, nil
}

// Get returns a specific scenario by ID, verifying user ownership
func (s *Service) Get(ctx context.Context, userID, scenarioID string) (*Scenario, error) {
	query := `
		SELECT id, name, description, parameters, results, tags, favorite, "hasAIParameters", "createdAt", "lastModified"
		FROM scenarios
		WHERE id = $1 AND "userId" = $2
	`

	var sc dbScenario
	err := s.db.Main.QueryRow(ctx, query, scenarioID, userID).Scan(
		&sc.ID,
		&sc.Name,
		&sc.Description,
		&sc.Parameters,
		&sc.Results,
		&sc.Tags,
		&sc.Favorite,
		&sc.HasAIParameters,
		&sc.CreatedAt,
		&sc.LastModified,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScenarioNotFound
		}
		return nil, err
	}

	scenario := sc.toScenario()
	return &scenario, nil
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

	now := time.Now()

	query := `
		INSERT INTO scenarios (id, "userId", name, description, parameters, results, tags, favorite, "hasAIParameters", "createdAt", "lastModified")
		VALUES ($1, $2, $3, $4, $5, $6, $7, false, $8, $9, $10)
		RETURNING id, name, description, parameters, results, tags, favorite, "hasAIParameters", "createdAt", "lastModified"
	`

	var sc dbScenario
	err := s.db.Main.QueryRow(ctx, query,
		id,
		userID,
		input.Name,
		input.Description,
		input.Parameters,
		input.Results,
		tags,
		input.HasAIParameters,
		now,
		now,
	).Scan(
		&sc.ID,
		&sc.Name,
		&sc.Description,
		&sc.Parameters,
		&sc.Results,
		&sc.Tags,
		&sc.Favorite,
		&sc.HasAIParameters,
		&sc.CreatedAt,
		&sc.LastModified,
	)
	if err != nil {
		return nil, err
	}

	scenario := sc.toScenario()
	return &scenario, nil
}

// Update updates an existing scenario
func (s *Service) Update(ctx context.Context, userID, scenarioID string, input UpdateScenarioInput) (*Scenario, error) {
	// First verify ownership
	existsQuery := `SELECT id FROM scenarios WHERE id = $1 AND "userId" = $2`
	var existingID string
	err := s.db.Main.QueryRow(ctx, existsQuery, scenarioID, userID).Scan(&existingID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScenarioNotFound
		}
		return nil, err
	}

	// Build dynamic update query
	// We use a simpler approach: update all provided fields
	query := `
		UPDATE scenarios SET
			name = COALESCE($3, name),
			description = COALESCE($4, description),
			parameters = COALESCE($5, parameters),
			results = COALESCE($6, results),
			tags = COALESCE($7, tags),
			favorite = COALESCE($8, favorite),
			"hasAIParameters" = COALESCE($9, "hasAIParameters"),
			"lastModified" = NOW()
		WHERE id = $1 AND "userId" = $2
		RETURNING id, name, description, parameters, results, tags, favorite, "hasAIParameters", "createdAt", "lastModified"
	`

	var sc dbScenario
	err = s.db.Main.QueryRow(ctx, query,
		scenarioID,
		userID,
		input.Name,
		input.Description,
		input.Parameters,
		input.Results,
		input.Tags,
		input.Favorite,
		input.HasAIParameters,
	).Scan(
		&sc.ID,
		&sc.Name,
		&sc.Description,
		&sc.Parameters,
		&sc.Results,
		&sc.Tags,
		&sc.Favorite,
		&sc.HasAIParameters,
		&sc.CreatedAt,
		&sc.LastModified,
	)
	if err != nil {
		return nil, err
	}

	scenario := sc.toScenario()
	return &scenario, nil
}

// Delete deletes a scenario
func (s *Service) Delete(ctx context.Context, userID, scenarioID string) error {
	// First verify ownership
	existsQuery := `SELECT id FROM scenarios WHERE id = $1 AND "userId" = $2`
	var existingID string
	err := s.db.Main.QueryRow(ctx, existsQuery, scenarioID, userID).Scan(&existingID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrScenarioNotFound
		}
		return err
	}

	query := `DELETE FROM scenarios WHERE id = $1 AND "userId" = $2`
	_, err = s.db.Main.Exec(ctx, query, scenarioID, userID)
	return err
}

// dbScenario is the database representation of a scenario
type dbScenario struct {
	ID              string
	Name            string
	Description     *string
	Parameters      map[string]interface{}
	Results         map[string]interface{}
	Tags            []string
	Favorite        bool
	HasAIParameters bool
	CreatedAt       time.Time
	LastModified    time.Time
}

// toScenario converts a database scenario to API scenario
func (sc *dbScenario) toScenario() Scenario {
	tags := sc.Tags
	if tags == nil {
		tags = []string{}
	}

	return Scenario{
		ID:              sc.ID,
		Name:            sc.Name,
		Description:     sc.Description,
		Parameters:      sc.Parameters,
		Results:         sc.Results,
		Tags:            tags,
		Favorite:        sc.Favorite,
		HasAIParameters: sc.HasAIParameters,
		CreatedAt:       sc.CreatedAt.Format(time.RFC3339),
		LastModified:    sc.LastModified.Format(time.RFC3339),
	}
}
