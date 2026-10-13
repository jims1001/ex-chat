package modruntime

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Each request has a pinned, distinct physical SQL connection before the start
// barrier. No single-connection pool or application mutex hides database races.
func cannedConcurrent(t *testing.T, f *cannedReadFixture, actions []func(*cannedReadFixture) int) []int {
	t.Helper()
	ready := sync.WaitGroup{}
	ready.Add(len(actions))
	start := make(chan struct{})
	statuses := make([]int, len(actions))
	var done sync.WaitGroup
	done.Add(len(actions))
	errs := make([]error, len(actions))
	sqlDB, err := f.runtime.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(len(actions) + 2)
	for i, action := range actions {
		go func(i int, action func(*cannedReadFixture) int) {
			defer done.Done()
			errs[i] = f.runtime.DB.Connection(func(db *gorm.DB) error {
				r := &Runtime{Config: f.runtime.Config, DB: db, Contracts: NewContractRegistry("OPS")}
				r.Contracts.UseCommandReceipts(db)
				if err := r.RegisterBusinessContracts(); err != nil {
					ready.Done()
					return err
				}
				child := &cannedReadFixture{runtime: r, ten: f.ten}
				ready.Done()
				<-start
				statuses[i] = action(child)
				return nil
			})
		}(i, action)
	}
	ready.Wait()
	require.GreaterOrEqual(t, sqlDB.Stats().InUse, len(actions))
	close(start)
	done.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	sort.Ints(statuses)
	return statuses
}
func cannedConcurrency(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("same_key_converges_one_receipt", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		c := cannedCommand("same-key", "create")
		actions := make([]func(*cannedReadFixture) int, 4)
		for i := range actions {
			actions[i] = func(f *cannedReadFixture) int { return f.write(c).Code }
		}
		require.Equal(t, []int{200, 200, 200, 200}, cannedConcurrent(t, f, actions))
		cannedCounts(t, f, 1, 1, 1)
	})
	t.Run("same_version_single_winner", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		row := cannedCreated(t, f, "seed")
		var actions []func(*cannedReadFixture) int
		for i := 0; i < 4; i++ {
			c := cannedCommand(fmt.Sprint("cas", i), "update")
			cannedTarget(&c, row.ID, 1)
			c.Input["content"] = fmt.Sprint("writer", i)
			actions = append(actions, func(f *cannedReadFixture) int { return f.write(c).Code })
		}
		require.Equal(t, []int{200, 409, 409, 409}, cannedConcurrent(t, f, actions))
		cannedCounts(t, f, 1, 2, 2)
	})
	t.Run("normalized_duplicate_single_winner", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		var actions []func(*cannedReadFixture) int
		for i, code := range []string{"MiXeD", " mixed ", "\u2003MIXED", "MIXED\t"} {
			c := cannedCommand(fmt.Sprint("duplicate", i), "create")
			c.Input["short_code"] = code
			actions = append(actions, func(f *cannedReadFixture) int { return f.write(c).Code })
		}
		require.Equal(t, []int{200, 409, 409, 409}, cannedConcurrent(t, f, actions))
		cannedCounts(t, f, 1, 1, 1)
	})
	t.Run("legacy_create_and_runtime_create_share_lock", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		statuses := cannedConcurrent(t, f, []func(*cannedReadFixture) int{
			func(f *cannedReadFixture) int { return f.write(cannedCommand("runtime", "create")).Code },
			func(f *cannedReadFixture) int {
				row := domain.CannedResponse{AccountID: 91, ShortCode: " HELLO ", Content: "legacy"}
				err := repository.NewCannedResponseRepository(f.runtime.DB).Create(&row)
				if err == repository.ErrCannedResponseDuplicate {
					return 409
				}
				if err != nil {
					return 500
				}
				return 200
			},
		})
		require.Equal(t, []int{200, 409}, statuses)
		var count int64
		require.NoError(t, f.runtime.DB.Model(&domain.CannedResponse{}).Count(&count).Error)
		require.Equal(t, int64(1), count)
	})
	t.Run("legacy_update_and_runtime_create_share_lock", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		legacy := domain.CannedResponse{AccountID: 91, ShortCode: "other", Content: "legacy"}
		require.NoError(t, f.runtime.DB.Create(&legacy).Error)
		statuses := cannedConcurrent(t, f, []func(*cannedReadFixture) int{
			func(f *cannedReadFixture) int { return f.write(cannedCommand("runtime", "create")).Code },
			func(f *cannedReadFixture) int {
				row := legacy
				row.ShortCode = " HELLO "
				err := repository.NewCannedResponseRepository(f.runtime.DB).Update(&row)
				if err == repository.ErrCannedResponseDuplicate {
					return 409
				}
				if err != nil {
					return 500
				}
				return 200
			},
		})
		require.Equal(t, []int{200, 409}, statuses)
		var rows []domain.CannedResponse
		require.NoError(t, f.runtime.DB.Find(&rows).Error)
		matches := 0
		for _, row := range rows {
			if repository.NormalizeCannedShortcode(row.ShortCode) == "hello" {
				matches++
			}
		}
		require.Equal(t, 1, matches)
	})
	t.Run("update_delete_CAS_single_winner", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		row := cannedCreated(t, f, "seed")
		var actions []func(*cannedReadFixture) int
		for _, op := range []string{"update", "delete"} {
			c := cannedCommand(op, op)
			cannedTarget(&c, row.ID, 1)
			actions = append(actions, func(f *cannedReadFixture) int { return f.write(c).Code })
		}
		statuses := cannedConcurrent(t, f, actions)
		require.Equal(t, 200, statuses[0])
		require.Contains(t, []int{404, 409}, statuses[1])
		var count int64
		require.NoError(t, f.runtime.DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&count).Error)
		require.Equal(t, int64(2), count)
	})
	t.Run("detail_versioned_is_one_SQL_snapshot", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		row := cannedCreated(t, f, "seed")
		q := cannedReadQuery("detail_versioned")
		q.Fields = append(q.Fields, "object_version")
		q.Filter["id"] = row.ID
		statuses := cannedConcurrent(t, f, []func(*cannedReadFixture) int{
			func(f *cannedReadFixture) int {
				for i := 1; i <= 12; i++ {
					c := cannedCommand(fmt.Sprint("update", i), "update")
					cannedTarget(&c, row.ID, uint64(i))
					c.Input["content"] = fmt.Sprint(i + 1)
					if out := f.write(c); out.Code != 200 {
						return out.Code
					}
				}
				return 200
			},
			func(f *cannedReadFixture) int {
				for i := 0; i < 24; i++ {
					out := f.request(q, nil)
					if out.Code != 200 {
						return out.Code
					}
					var body struct{ Data cannedVersionView }
					if json.Unmarshal(out.Body.Bytes(), &body) != nil {
						return 500
					}
					if body.Data.ObjectVersion > 1 && body.Data.Content != fmt.Sprint(body.Data.ObjectVersion) {
						return 599
					}
				}
				return 200
			},
		})
		require.Equal(t, []int{200, 200}, statuses)
	})
}
func TestOPSCannedResponseConcurrency(t *testing.T)         { cannedConcurrency(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseConcurrency(t *testing.T) { cannedConcurrency(t, postgresModuleDB) }
