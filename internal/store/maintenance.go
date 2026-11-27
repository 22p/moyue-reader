package store

import "time"

// dailyRetentionDays bounds how long per-day reading stats are kept. The stats
// endpoint only needs about a year of history.
const dailyRetentionDays = 400

// VACUUM rewrites the whole database file, so only run it when enough free
// pages have accumulated to make the rewrite worthwhile.
const (
	vacuumMinFreePages = 512
	vacuumMinFreeRatio = 10
)

// Maintain deletes stale rows and compacts the database when it has grown
// sparse. It is safe to call at startup and on a low-frequency schedule.
func (s *Store) Maintain() error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, nowMS()); err != nil {
		return err
	}
	cutoff := time.Now().AddDate(0, 0, -dailyRetentionDays).Format("2006-01-02")
	if _, err := s.db.Exec(`DELETE FROM daily WHERE day < ?`, cutoff); err != nil {
		return err
	}
	return s.compact()
}

// compact reclaims space after large deletions. It is a no-op while the free
// list is small so regular maintenance does not block readers.
func (s *Store) compact() error {
	var free int64
	if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		return err
	}
	if free < vacuumMinFreePages {
		return nil
	}
	var total int64
	if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&total); err != nil {
		return err
	}
	if total <= 0 || free*100/total < vacuumMinFreeRatio {
		return nil
	}
	_, err := s.db.Exec(`VACUUM`)
	return err
}
