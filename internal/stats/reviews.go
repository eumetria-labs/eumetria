package stats

import (
	"math"
	"sort"
	"time"

	"eumetria/internal/store"
)

// Review is a published weekly review (Markdown, written by Claude).
type Review struct {
	Week      string    `json:"week"` // Monday
	Text      string    `json:"text"`
	TextDE    string    `json:"text_de,omitempty"` // German, when written
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
}

// ReviewStatus warns the site owner about the Anthropic credit: Billing when
// the API refused for money reasons, CreditLow when the credit bought
// (AI_CREDIT_USD) minus the reviews' cost covers only a few more weeks.
// The remainder is an estimate: other uses of the same credit don't count.
type ReviewStatus struct {
	Billing      bool    `json:"billing,omitempty"`
	CreditLow    bool    `json:"credit_low,omitempty"`
	RemainingUSD float64 `json:"remaining_usd,omitempty"`
	WeeksLeft    int     `json:"weeks_left,omitempty"`
}

// lowWeeks is when the credit counts as low: about a month of reviews.
const lowWeeks = 4

func reviews(h *store.History) []Review {
	var out []Review
	for _, r := range h.Reviews {
		out = append(out, Review{Week: r.Week, Text: r.Text, TextDE: r.TextDE, Model: r.Model, CreatedAt: r.CreatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Week > out[j].Week })
	return out
}

func reviewStatus(h *store.History, creditUSD float64) *ReviewStatus {
	st := &ReviewStatus{Billing: h.ReviewState != nil && h.ReviewState.Billing}
	if creditUSD > 0 {
		var all []*store.Review
		for _, r := range h.Reviews {
			all = append(all, r)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].Week > all[j].Week })
		var spent, recent float64
		n := 0
		for i, r := range all {
			spent += r.CostUSD
			if i < 4 && r.CostUSD > 0 {
				recent += r.CostUSD
				n++
			}
		}
		st.RemainingUSD = math.Max(0, math.Round((creditUSD-spent)*100)/100)
		if n > 0 {
			// The tolerance keeps 0.30/0.10 from coming out as 2.999… weeks.
			st.WeeksLeft = int(math.Floor(st.RemainingUSD/(recent/float64(n)) + 1e-9))
			st.CreditLow = st.WeeksLeft < lowWeeks
		}
	}
	if !st.Billing && !st.CreditLow {
		return nil
	}
	return st
}
