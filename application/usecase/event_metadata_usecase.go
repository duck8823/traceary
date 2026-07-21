package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
)

// EventMetadataUsecase exposes body-free event reads to presentation adapters.
type EventMetadataUsecase interface {
	List(ctx context.Context, criteria apptypes.EventListCriteria) ([]apptypes.EventMetadata, error)
	Search(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error)
	Context(ctx context.Context, criteria apptypes.EventContextCriteria) ([]apptypes.EventMetadata, error)
}

type eventMetadataUsecase struct {
	query queryservice.EventMetadataQueryService
}

// NewEventMetadataUsecase creates the body-free event read usecase.
func NewEventMetadataUsecase(query queryservice.EventMetadataQueryService) EventMetadataUsecase {
	return &eventMetadataUsecase{query: query}
}

func (u *eventMetadataUsecase) List(ctx context.Context, criteria apptypes.EventListCriteria) ([]apptypes.EventMetadata, error) {
	if u.query == nil {
		return nil, xerrors.Errorf("event metadata query service is not configured")
	}
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}

	metadata, err := u.query.ListRecentMetadata(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to list event metadata: %w", err)
	}
	return metadata, nil
}

func (u *eventMetadataUsecase) Search(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	if u.query == nil {
		return nil, xerrors.Errorf("event metadata query service is not configured")
	}
	if !hasSearchConstraint(criteria) {
		return nil, xerrors.Errorf("at least one search filter is required")
	}
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}
	resolvedKind, err := resolveOptionalSearchKind(criteria.Kind().String())
	if err != nil {
		return nil, err
	}
	resolvedCriteria := apptypes.NewEventSearchCriteriaBuilder(criteria.Limit()).
		Query(criteria.Query()).
		Workspace(criteria.Workspace()).
		SessionID(criteria.SessionID()).
		Client(criteria.Client()).
		Agent(criteria.Agent()).
		Kind(resolvedKind).
		From(criteria.From()).
		To(criteria.To()).
		Offset(criteria.Offset()).
		FailuresOnly(criteria.FailuresOnly()).
		Build()

	metadata, err := u.query.SearchMetadata(ctx, resolvedCriteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to search event metadata: %w", err)
	}
	return metadata, nil
}

func (u *eventMetadataUsecase) Context(ctx context.Context, criteria apptypes.EventContextCriteria) ([]apptypes.EventMetadata, error) {
	if u.query == nil {
		return nil, xerrors.Errorf("event metadata query service is not configured")
	}
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}

	metadata, err := u.query.GetContextMetadata(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("failed to get context metadata: %w", err)
	}
	return metadata, nil
}
