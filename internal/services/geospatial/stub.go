package geospatial

import "context"

// Stub is a placeholder implementation that returns ErrNotConfigured.
// It is used until a real geo-spatial provider (e.g., ATTOM, CoreLogic) is wired.
type Stub struct{}

// NewStub returns a stub geo-spatial service.
func NewStub() *Stub {
	return &Stub{}
}

func (s *Stub) GetComparableSales(_ context.Context, _ ComparableSalesRequest) (*ComparableSalesResponse, error) {
	return nil, ErrNotConfigured
}

func (s *Stub) GetComparableRentals(_ context.Context, _ ComparableRentalsRequest) (*ComparableRentalsResponse, error) {
	return nil, ErrNotConfigured
}

func (s *Stub) IsConfigured() bool {
	return false
}
