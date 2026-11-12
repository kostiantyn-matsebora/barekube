package store

import (
	"database/sql"

	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

// Observations persists each node's newest observation.
type Observations struct{ db *sql.DB }

var _ observe.Persistence = (*Observations)(nil)

// Observations returns the node observation store.
func (d *DB) Observations() *Observations { return &Observations{db: d.sql} }

// SaveObservation replaces the node's row.
func (s *Observations) SaveObservation(o observe.Observation) error {
	_, err := s.db.Exec(`
INSERT INTO node_observations(node, at, reachable, k3s, ready, cordoned) VALUES(?,?,?,?,?,?)
ON CONFLICT(node) DO UPDATE SET at=excluded.at, reachable=excluded.reachable, k3s=excluded.k3s,
  ready=excluded.ready, cordoned=excluded.cordoned`,
		o.Node, tn(o.At), int(o.Reachable), int(o.K3sActive), int(o.Ready), int(o.Cordoned))
	return err
}

// LoadObservations returns every saved row.
func (s *Observations) LoadObservations() ([]observe.Observation, error) {
	rows, err := s.db.Query("SELECT node, at, reachable, k3s, ready, cordoned FROM node_observations ORDER BY node")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []observe.Observation
	for rows.Next() {
		var (
			o          observe.Observation
			at         int64
			r, k, y, c int
		)
		if err := rows.Scan(&o.Node, &at, &r, &k, &y, &c); err != nil {
			return nil, err
		}
		o.At = ft(at)
		o.Reachable, o.K3sActive, o.Ready, o.Cordoned = observe.Tri(r), observe.Tri(k), observe.Tri(y), observe.Tri(c)
		out = append(out, o)
	}
	return out, rows.Err()
}
