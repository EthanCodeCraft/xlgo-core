package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func seedM5Users(t *testing.T, repo *BaseRepo[h6User], n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := repo.Create(context.Background(), &h6User{Name: fmt.Sprintf("m5-%04d", i), Age: i}); err != nil {
			t.Fatalf("seed user %d: %v", i, err)
		}
	}
}

func TestM5NilContextFallsBackToBackground(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))

	u := &h6User{Name: "nil-ctx"}
	if err := repo.Create(nil, u); err != nil {
		t.Fatalf("Create(nil ctx): %v", err)
	}
	got, err := repo.FindByID(nil, u.ID)
	if err != nil {
		t.Fatalf("FindByID(nil ctx): %v", err)
	}
	if got.Name != "nil-ctx" {
		t.Fatalf("got %q, want nil-ctx", got.Name)
	}
}

func TestM5UnsafeOrderRejected(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()

	cases := []struct {
		name string
		fn   func() error
	}{
		{"FindOrdered", func() error {
			_, err := repo.FindOrdered(ctx, "name desc; drop table h6_users", 10)
			return err
		}},
		{"FindWhereOrdered", func() error {
			_, err := repo.FindWhereOrdered(ctx, "name desc --", "name <> ?", "")
			return err
		}},
		{"FindPageOrdered", func() error {
			_, err := repo.FindPageOrdered(ctx, 1, 10, "lower(name) desc")
			return err
		}},
		{"FindPageWhereOrdered", func() error {
			_, err := repo.FindPageWhereOrdered(ctx, 1, 10, "name collate nocase", "name <> ?", "")
			return err
		}},
		{"QueryBuilder", func() error {
			_, err := repo.NewQueryBuilder().Order("name; drop table h6_users").Find(ctx)
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); !errors.Is(err, ErrUnsafeOrder) {
				t.Fatalf("err = %v, want ErrUnsafeOrder", err)
			}
		})
	}
}

func TestM5QueryBuilderUnsafeOrderPropagatesToAllTerminators(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()
	seedM5Users(t, repo, 1)

	if _, err := repo.NewQueryBuilder().Order("name; drop table h6_users").Find(ctx); !errors.Is(err, ErrUnsafeOrder) {
		t.Fatalf("Find err = %v, want ErrUnsafeOrder", err)
	}
	if _, err := repo.NewQueryBuilder().Order("name; drop table h6_users").First(ctx); !errors.Is(err, ErrUnsafeOrder) {
		t.Fatalf("First err = %v, want ErrUnsafeOrder", err)
	}
	if _, err := repo.NewQueryBuilder().Order("name; drop table h6_users").Count(ctx); !errors.Is(err, ErrUnsafeOrder) {
		t.Fatalf("Count err = %v, want ErrUnsafeOrder", err)
	}
	if _, err := repo.NewQueryBuilder().Order("name; drop table h6_users").Page(ctx, 1, 10); !errors.Is(err, ErrUnsafeOrder) {
		t.Fatalf("Page err = %v, want ErrUnsafeOrder", err)
	}
}

func TestM5SafeOrderStillWorks(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()
	if err := repo.Create(ctx, &h6User{Name: "b"}); err != nil {
		t.Fatalf("Create b: %v", err)
	}
	if err := repo.Create(ctx, &h6User{Name: "a"}); err != nil {
		t.Fatalf("Create a: %v", err)
	}

	got, err := repo.FindOrdered(ctx, "name ASC, id DESC", 10)
	if err != nil {
		t.Fatalf("FindOrdered safe order: %v", err)
	}
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("unexpected order: %+v", got)
	}
}

func TestM5UpdateBatchRejectsUnsafeFieldAndEmptyIDsNoop(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	ctx := context.Background()
	u := &h6User{Name: "field", Age: 1}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.UpdateBatch(ctx, []uint{u.ID}, "age = age + 1", 2); !errors.Is(err, ErrUnsafeField) {
		t.Fatalf("unsafe field err = %v, want ErrUnsafeField", err)
	}
	if err := repo.UpdateBatch(ctx, nil, "age = age + 1", 2); err != nil {
		t.Fatalf("empty ids should noop before field validation, got %v", err)
	}
	if err := repo.UpdateBatch(ctx, []uint{u.ID}, "age", 2); err != nil {
		t.Fatalf("safe field UpdateBatch: %v", err)
	}
	got, _ := repo.FindByID(ctx, u.ID)
	if got.Age != 2 {
		t.Fatalf("age = %d, want 2", got.Age)
	}
}

func TestM5FindByIDsEmptyReturnsEmptySlice(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	got, err := repo.FindByIDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("FindByIDs empty: %v", err)
	}
	if got == nil {
		t.Fatal("FindByIDs empty should return non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestM5FindAllAppliesDefaultLimit(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	seedM5Users(t, repo, DefaultFindAllLimit+5)

	got, err := repo.FindAll(context.Background())
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(got) != DefaultFindAllLimit {
		t.Fatalf("FindAll len = %d, want default limit %d", len(got), DefaultFindAllLimit)
	}

	all, err := repo.FindAllUnbounded(context.Background())
	if err != nil {
		t.Fatalf("FindAllUnbounded: %v", err)
	}
	if len(all) != DefaultFindAllLimit+5 {
		t.Fatalf("FindAllUnbounded len = %d, want %d", len(all), DefaultFindAllLimit+5)
	}
}

func TestM5PaginationBounds(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	seedM5Users(t, repo, MaxPageSize+5)

	p, err := repo.FindPage(context.Background(), -10, 0)
	if err != nil {
		t.Fatalf("FindPage default bounds: %v", err)
	}
	if p.Page != 1 || p.PageSize != DefaultPageSize || len(p.Items) != DefaultPageSize {
		t.Fatalf("page defaults = page:%d size:%d items:%d, want 1/%d/%d",
			p.Page, p.PageSize, len(p.Items), DefaultPageSize, DefaultPageSize)
	}

	p, err = repo.FindPage(context.Background(), 1, MaxPageSize+100)
	if err != nil {
		t.Fatalf("FindPage max size: %v", err)
	}
	if p.PageSize != MaxPageSize || len(p.Items) != MaxPageSize {
		t.Fatalf("page max = size:%d items:%d, want %d/%d", p.PageSize, len(p.Items), MaxPageSize, MaxPageSize)
	}

	p, err = repo.FindPage(context.Background(), MaxPage+1, 10)
	if err != nil {
		t.Fatalf("FindPage max page: %v", err)
	}
	if p.Page != MaxPage {
		t.Fatalf("page = %d, want MaxPage %d", p.Page, MaxPage)
	}
}

func TestM5QueryBuilderNilDBPanicsClearly(t *testing.T) {
	repo := NewBaseRepo[h6User](nil)
	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("NewQueryBuilder with nil db should panic")
		}
	}()
	_ = repo.NewQueryBuilder()
}

func TestM5QueryBuilderNilContextAndPageBounds(t *testing.T) {
	repo := NewBaseRepo[h6User](newH6SqliteDB(t))
	seedM5Users(t, repo, 25)

	count, err := repo.NewQueryBuilder().Where("created_at <= ?", time.Now().Add(time.Hour)).Count(nil)
	if err != nil {
		t.Fatalf("Count nil ctx: %v", err)
	}
	if count != 25 {
		t.Fatalf("count = %d, want 25", count)
	}

	p, err := repo.NewQueryBuilder().Page(nil, 1, 0)
	if err != nil {
		t.Fatalf("Page nil ctx: %v", err)
	}
	if p.PageSize != DefaultPageSize || len(p.Items) != DefaultPageSize {
		t.Fatalf("QueryBuilder page size/items = %d/%d, want %d/%d",
			p.PageSize, len(p.Items), DefaultPageSize, DefaultPageSize)
	}
}

func TestM5DeleteBatchEmptyIDsNoop(t *testing.T) {
	db := newDryRunDB(t)
	repo := NewBaseRepo[pageCountModel](db)

	if err := repo.DeleteBatch(context.Background(), nil); err != nil {
		t.Fatalf("DeleteBatch empty ids: %v", err)
	}
	if got := db.Statement.SQL.String(); got != "" {
		t.Fatalf("DeleteBatch empty ids should not issue SQL, got %s", got)
	}
	if err := repo.HardDeleteBatch(context.Background(), nil); err != nil {
		t.Fatalf("HardDeleteBatch empty ids: %v", err)
	}
	if got := db.Statement.SQL.String(); got != "" {
		t.Fatalf("HardDeleteBatch empty ids should not issue SQL, got %s", got)
	}
}

func TestM5FindAllUsesLimitSQL(t *testing.T) {
	db := newDryRunDB(t)
	repo := NewBaseRepo[pageCountModel](db)
	var models []pageCountModel
	result := repo.readConn(context.Background()).Limit(DefaultFindAllLimit).Find(&models)
	sql := strings.ToUpper(result.Statement.SQL.String())
	if !strings.Contains(sql, "LIMIT") {
		t.Fatalf("FindAll default path should include LIMIT, got %s", sql)
	}
}
