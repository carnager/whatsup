package main

import (
	"reflect"

	qt "github.com/mappu/miqt/qt6"
)

// ListModel is a QAbstractListModel backed by Go rows. Rows are keyed so
// updates can be applied as fine-grained inserts/changes instead of resets,
// which keeps QML ListView scroll positions stable.
type ListModel struct {
	M     *qt.QAbstractListModel
	roles []string
	keys  []string
	rows  [][]any
}

const firstRole = int(qt.UserRole) + 1

func NewListModel(roles ...string) *ListModel {
	lm := &ListModel{M: qt.NewQAbstractListModel(), roles: roles}
	lm.M.OnRowCount(func(parent *qt.QModelIndex) int {
		if parent.IsValid() {
			return 0
		}
		return len(lm.rows)
	})
	lm.M.OnRoleNames(func(super func() map[int][]byte) map[int][]byte {
		out := map[int][]byte{}
		for i, r := range lm.roles {
			out[firstRole+i] = []byte(r)
		}
		return out
	})
	lm.M.OnData(func(idx *qt.QModelIndex, role int) *qt.QVariant {
		r, c := idx.Row(), role-firstRole
		if !idx.IsValid() || r < 0 || r >= len(lm.rows) || c < 0 || c >= len(lm.roles) {
			return qt.NewQVariant()
		}
		switch v := lm.rows[r][c].(type) {
		case string:
			return qt.NewQVariant14(v)
		case int:
			return qt.NewQVariant4(v)
		case bool:
			return qt.NewQVariant8(v)
		}
		return qt.NewQVariant()
	})
	return lm
}

func (lm *ListModel) Len() int { return len(lm.rows) }

func (lm *ListModel) Row(i int) []any { return lm.rows[i] }

func (lm *ListModel) idx(i int) *qt.QModelIndex {
	return lm.M.Index(i, 0, qt.NewQModelIndex())
}

func (lm *ListModel) Reset(keys []string, rows [][]any) {
	lm.M.BeginResetModel()
	lm.keys, lm.rows = keys, rows
	lm.M.EndResetModel()
}

// Update applies a new state. It handles the common cases (rows added at the
// front or back, rows dropped from the back, rows changed in place) with
// minimal signals, and falls back to a reset otherwise.
func (lm *ListModel) Update(keys []string, rows [][]any) {
	old := lm.keys
	if len(old) == 0 || len(keys) == 0 {
		lm.Reset(keys, rows)
		return
	}
	p := -1
	for i, k := range keys {
		if k == old[0] {
			p = i
			break
		}
	}
	if p < 0 {
		lm.Reset(keys, rows)
		return
	}
	overlap := min(len(old), len(keys)-p)
	for i := 0; i < overlap; i++ {
		if keys[p+i] != old[i] {
			lm.Reset(keys, rows)
			return
		}
	}
	// drop rows from the back that are gone
	if overlap < len(old) {
		lm.M.BeginRemoveRows(qt.NewQModelIndex(), overlap, len(old)-1)
		lm.keys, lm.rows = lm.keys[:overlap], lm.rows[:overlap]
		lm.M.EndRemoveRows()
	}
	// changed rows
	for i := 0; i < overlap; i++ {
		if !reflect.DeepEqual(lm.rows[i], rows[p+i]) {
			lm.rows[i] = rows[p+i]
			ix := lm.idx(i)
			lm.M.DataChanged(ix, ix)
		}
	}
	// new rows in front
	if p > 0 {
		lm.M.BeginInsertRows(qt.NewQModelIndex(), 0, p-1)
		lm.keys = append(append([]string{}, keys[:p]...), lm.keys...)
		lm.rows = append(append([][]any{}, rows[:p]...), lm.rows...)
		lm.M.EndInsertRows()
	}
	// new rows at the back
	if n := len(keys); n > p+overlap {
		lm.M.BeginInsertRows(qt.NewQModelIndex(), len(lm.rows), len(lm.rows)+n-(p+overlap)-1)
		lm.keys = append(lm.keys, keys[p+overlap:]...)
		lm.rows = append(lm.rows, rows[p+overlap:]...)
		lm.M.EndInsertRows()
	}
}
