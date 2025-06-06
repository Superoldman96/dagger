package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/moby/buildkit/identity"
)

type engineSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &engineSchema{}

func (s *engineSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("engine", s.engine).
			Doc("The Dagger engine container configuration and state"),
	}.Install(s.srv)

	dagql.Fields[*core.Engine]{
		dagql.Func("localCache", s.localCache).
			Doc("The local (on-disk) cache for the Dagger engine"),
	}.Install(s.srv)

	dagql.Fields[*core.EngineCache]{
		dagql.NodeFuncWithCacheKey("entrySet", s.cacheEntrySet, dagql.CachePerCall).
			Doc("The current set of entries in the cache"),
		dagql.Func("prune", s.cachePrune).
			DoNotCache("Mutates mutable state").
			Doc("Prune the cache of releaseable entries").
			Args(
				dagql.Arg("useDefaultPolicy").Doc("Use the engine-wide default pruning policy if true, otherwise prune the whole cache of any releasable entries."),
			),
	}.Install(s.srv)

	dagql.Fields[*core.EngineCacheEntrySet]{
		dagql.Func("entries", s.cacheEntrySetEntries).
			Doc("The list of individual cache entries in the set"),
	}.Install(s.srv)

	dagql.Fields[*core.EngineCacheEntry]{}.Install(s.srv)
}

func (s *engineSchema) engine(ctx context.Context, parent *core.Query, args struct{}) (*core.Engine, error) {
	return &core.Engine{Query: parent}, nil
}

func (s *engineSchema) localCache(ctx context.Context, parent *core.Engine, args struct{}) (*core.EngineCache, error) {
	if err := parent.Query.RequireMainClient(ctx); err != nil {
		return nil, err
	}
	policy := parent.Query.Clone().EngineLocalCachePolicy()
	if policy == nil {
		return &core.EngineCache{Query: parent.Query}, nil
	}
	return &core.EngineCache{
		Query:         parent.Query,
		ReservedSpace: int(policy.ReservedSpace),
		TargetSpace:   int(policy.TargetSpace),
		MaxUsedSpace:  int(policy.MaxUsedSpace),
		MinFreeSpace:  int(policy.MinFreeSpace),
		KeepBytes:     int(policy.ReservedSpace),
	}, nil
}

func (s *engineSchema) cacheEntrySet(ctx context.Context, parent dagql.Instance[*core.EngineCache], args struct {
	Key string `default:""`
}) (inst dagql.Instance[*core.EngineCacheEntrySet], _ error) {
	if err := parent.Self.Query.RequireMainClient(ctx); err != nil {
		return inst, err
	}

	if args.Key == "" {
		err := s.srv.Select(ctx, parent, &inst,
			dagql.Selector{
				Field: "entrySet",
				Args: []dagql.NamedInput{
					{
						Name:  "key",
						Value: dagql.NewString(identity.NewID()),
					},
				},
			},
		)
		return inst, err
	}

	entrySet, err := parent.Self.Query.EngineLocalCacheEntries(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to load cache entries: %w", err)
	}

	return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, entrySet)
}

func (s *engineSchema) cachePrune(ctx context.Context, parent *core.EngineCache, args struct {
	UseDefaultPolicy bool `default:"false"`
}) (dagql.Nullable[core.Void], error) {
	void := dagql.Null[core.Void]()
	if err := parent.Query.RequireMainClient(ctx); err != nil {
		return void, err
	}

	_, err := parent.Query.PruneEngineLocalCacheEntries(ctx, args.UseDefaultPolicy)
	if err != nil {
		return void, fmt.Errorf("failed to prune cache entries: %w", err)
	}

	return void, nil
}

func (s *engineSchema) cacheEntrySetEntries(ctx context.Context, parent *core.EngineCacheEntrySet, args struct{}) ([]*core.EngineCacheEntry, error) {
	return parent.EntriesList, nil
}
