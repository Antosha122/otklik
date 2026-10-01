package store

import (
	"context"
	"time"
)

// DayCount — один день дашборда: сколько обращений создано и завершено.
type DayCount struct {
	Day       string `json:"day"` // YYYY-MM-DD
	Created   int    `json:"created"`
	Completed int    `json:"completed"`
}

// AdminDailySeries — дневная серия для графика динамики на дашборде админа.
// nil-границы периода означают «последние 30 дней»; окно ограничено
// maxDays днями (от последнего дня периода назад), чтобы график не растягивался.
func (st *Store) AdminDailySeries(ctx context.Context, from, to *time.Time, maxDays int) ([]DayCount, error) {
	if maxDays <= 0 {
		maxDays = 30
	}
	if to == nil {
		now := time.Now()
		to = &now
	}
	// «до» эксклюзивно (обычно полночь следующего дня): последний день
	// окна — дата секундами раньше; для «сейчас» это сегодняшний день.
	lastDay := to.Add(-time.Second)
	if from == nil || to.Sub(*from) > time.Duration(maxDays)*24*time.Hour {
		f := lastDay.AddDate(0, 0, -(maxDays - 1))
		from = &f
	}
	rows, err := st.DB.QueryContext(ctx, `
		WITH days AS (
			SELECT generate_series($1::date, $2::date, interval '1 day')::date AS d
		), cr AS (
			SELECT a.created_at::date AS d, count(*) AS n
			FROM appeals a
			WHERE a.created_at >= $1 AND a.created_at < $3
			GROUP BY 1
		), done AS (
			SELECT a.updated_at::date AS d, count(*) AS n
			FROM appeals a
			WHERE a.status = 'completed' AND a.updated_at >= $1 AND a.updated_at < $3
			GROUP BY 1
		)
		SELECT to_char(days.d, 'YYYY-MM-DD'), COALESCE(cr.n, 0), COALESCE(done.n, 0)
		FROM days
		LEFT JOIN cr ON cr.d = days.d
		LEFT JOIN done ON done.d = days.d
		ORDER BY days.d`,
		*from, lastDay, *to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayCount
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Day, &d.Created, &d.Completed); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
