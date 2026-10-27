// Package store persists the fetched per-day history between sync runs.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Macros struct {
	Energy  float64 `json:"energy"`
	Protein float64 `json:"protein"`
	Carb    float64 `json:"carb"`
	Fat     float64 `json:"fat"`
}

func (m *Macros) Add(o Macros) {
	m.Energy += o.Energy
	m.Protein += o.Protein
	m.Carb += o.Carb
	m.Fat += o.Fat
}

// Item is one logged food entry. Amount is in Unit (g, ml or portion);
// Serving/ServingQty describe how it was entered, e.g. 2 × "slice".
type Item struct {
	Name       string  `json:"name"`
	Daytime    string  `json:"daytime"`
	Amount     float64 `json:"amount,omitempty"`
	Unit       string  `json:"unit,omitempty"`
	Serving    string  `json:"serving,omitempty"`
	ServingQty float64 `json:"serving_quantity,omitempty"`
	Energy     float64 `json:"energy"`
	// Protein, Carb and Fat are the entry's grams. Fiber, Sugar and
	// Saturated (fat) are set only when Yazio knows them: products and
	// recipes usually, AI entries never.
	Protein   float64  `json:"protein,omitempty"`
	Carb      float64  `json:"carb,omitempty"`
	Fat       float64  `json:"fat,omitempty"`
	Fiber     *float64 `json:"fiber,omitempty"`
	Sugar     *float64 `json:"sugar,omitempty"`
	Saturated *float64 `json:"saturated,omitempty"`
	AI        bool     `json:"ai,omitempty"`
	// EntryID is Yazio's ID of the diary entry.
	EntryID string `json:"entry_id,omitempty"`
	// RecipeID links a recipe portion to Foods["recipe:"+RecipeID], the
	// current version, and RecipeVersion to the version eaten that day
	// (RecipeKey). RecipeStale marks an entry whose day no longer adds up
	// with any stored version: the recipe changed and the version eaten is
	// not known.
	RecipeID      string `json:"recipe_id,omitempty"`
	RecipeVersion string `json:"recipe_version,omitempty"`
	RecipeStale   bool   `json:"recipe_stale,omitempty"`
	// EAN is the product's barcode, to look it up on Open Food Facts.
	EAN string `json:"ean,omitempty"`
}

// RecipeKey is the Foods key of one version of a recipe. Yazio edits
// recipes in place, so every version that was eaten is kept under its own
// key; the current one is also under "recipe:"+id.
func RecipeKey(id, version string) string {
	return "recipe:" + id + "@" + version
}

// Photo is a meal photo. File is the downloaded copy, relative to the photo
// directory; empty when the download failed.
type Photo struct {
	Daytime string `json:"daytime,omitempty"`
	URL     string `json:"url"`
	File    string `json:"file,omitempty"`
}

type Activity struct {
	Name     string  `json:"name"`
	Energy   float64 `json:"energy"`
	Duration float64 `json:"duration,omitempty"`
	Distance float64 `json:"distance,omitempty"`
	// ExternalID is the Google Health workout ID when Yazio imported the
	// activity from Fitbit (Workout.ID).
	ExternalID string `json:"external_id,omitempty"`
}

// DayVersion is bumped whenever Day gains fields that need a re-fetch; older
// days are then fetched again on the next run.
const DayVersion = 5 // 4: macros per entry, 5: sugar

type Day struct {
	Version        int                `json:"version,omitempty"`
	Date           string             `json:"date"`
	FetchedAt      time.Time          `json:"fetched_at"`
	Total          Macros             `json:"total"`
	Meals          map[string]Macros  `json:"meals"`
	Goals          map[string]float64 `json:"goals,omitempty"`
	Steps          float64            `json:"steps"`
	ActivityEnergy float64            `json:"activity_energy"`
	// ConsumeActivityEnergy reports whether Goals["energy.energy"] includes
	// ActivityEnergy; nil for days fetched before this was recorded.
	ConsumeActivityEnergy *bool      `json:"consume_activity_energy,omitempty"`
	Water                 float64    `json:"water"`
	Weight                *float64   `json:"weight,omitempty"`
	Items                 []Item     `json:"items,omitempty"`
	Activities            []Activity `json:"activities,omitempty"`
	Photos                []Photo    `json:"photos,omitempty"`
}

// FoodVersion is bumped when Food gains fields; older cached products and
// recipes are fetched again.
const FoodVersion = 1

// Food is a cached product or recipe lookup. Nutrients are per base unit for
// products and per portion for recipes.
type Food struct {
	Version   int                `json:"version,omitempty"`
	Name      string             `json:"name"`
	Unit      string             `json:"unit,omitempty"`
	Nutrients map[string]float64 `json:"nutrients,omitempty"`
	Missing   bool               `json:"missing,omitempty"`
	// Recipes only: the number of portions the ingredients make, the
	// ingredients and the recipe photo (ImageFile is the downloaded copy in
	// the photo directory, empty when the download failed).
	Portions    float64      `json:"portions,omitempty"`
	Ingredients []Ingredient `json:"ingredients,omitempty"`
	Image       string       `json:"image,omitempty"`
	ImageFile   string       `json:"image_file,omitempty"`
	// Hash identifies a recipe version by its content (see RecipeKey).
	Hash string `json:"hash,omitempty"`
	// EAN is a product's barcode (the first Yazio lists), if it has one.
	EAN string `json:"ean,omitempty"`
}

// Ingredient is one product of a recipe, in the amount the whole recipe
// uses; the macros are worked out from the product's nutrients.
type Ingredient struct {
	Name       string  `json:"name"`
	Producer   string  `json:"producer,omitempty"`
	Amount     float64 `json:"amount"`
	Unit       string  `json:"unit,omitempty"`
	Serving    string  `json:"serving,omitempty"`
	ServingQty float64 `json:"serving_quantity,omitempty"`
	EAN        string  `json:"ean,omitempty"`
	Macros
}

type History struct {
	Days  map[string]*Day  `json:"days"`
	Foods map[string]*Food `json:"foods"`
	// Health holds the Google Health (Fitbit) data per day, kept apart from
	// Days because the two sources are fetched independently.
	Health map[string]*HealthDay `json:"health,omitempty"`
	// HealthFrom is the first day the Google Health window covered; an
	// earlier -since triggers a backfill.
	HealthFrom string `json:"health_from,omitempty"`
	// Devices are the paired Fitbit devices as of the last sync.
	Devices []Device `json:"devices,omitempty"`
	// HealthSync is the outcome of the Google Health syncs, so the page can
	// warn when they keep failing.
	HealthSync *SyncState `json:"health_sync,omitempty"`
	// OFF caches Open Food Facts data by barcode.
	OFF map[string]*OFFProduct `json:"off,omitempty"`
	// Reviews are the weekly reviews written by Claude, by the week's
	// Monday; ReviewState records a failing attempt.
	Reviews     map[string]*Review `json:"reviews,omitempty"`
	ReviewState *ReviewState       `json:"review_state,omitempty"`
}

// Review is the AI review of one calendar week (Monday to Sunday).
type Review struct {
	Week         string    `json:"week"`              // Monday, YYYY-MM-DD
	Text         string    `json:"text"`              // Markdown, English
	TextDE       string    `json:"text_de,omitempty"` // the same in German; empty for older reviews
	Model        string    `json:"model"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CostUSD      float64   `json:"cost_usd,omitempty"` // estimate; 0 when the model's price is unknown
	CreatedAt    time.Time `json:"created_at"`
}

// ReviewState is the last failed attempt to write a review. Billing marks
// a refusal for money reasons: the credit is used up or the workspace's
// spend limit is reached.
type ReviewState struct {
	LastAttempt time.Time `json:"last_attempt"`
	Error       string    `json:"error"`
	Billing     bool      `json:"billing,omitempty"`
}

// OFFProduct is what Open Food Facts knows about a barcode. Its data is
// crowd-sourced: a grade is empty when the product isn't rated there.
type OFFProduct struct {
	Name  string `json:"name,omitempty"`
	Brand string `json:"brand,omitempty"`
	// NutriScore is "a" to "e", "not-applicable" (e.g. supplements, water)
	// or empty; NOVA is the processing group 1 to 4, 0 when unknown.
	NutriScore string    `json:"nutriscore,omitempty"`
	NOVA       int       `json:"nova,omitempty"`
	Additives  []string  `json:"additives,omitempty"` // E numbers, e.g. "E322"
	NotFound   bool      `json:"not_found,omitempty"`
	FetchedAt  time.Time `json:"fetched_at"`
}

// SyncState records the last attempt of a sync and whether it worked.
type SyncState struct {
	LastSuccess time.Time `json:"last_success,omitzero"`
	LastAttempt time.Time `json:"last_attempt"`
	Error       string    `json:"error,omitempty"`
	// Auth is true when the error needs a new authorization (-google-auth).
	Auth bool `json:"auth,omitempty"`
}

func Load(path string) (*History, error) {
	h := &History{Days: map[string]*Day{}, Foods: map[string]*Food{}, Health: map[string]*HealthDay{}, OFF: map[string]*OFFProduct{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, h); err != nil {
		return nil, err
	}
	if h.Days == nil {
		h.Days = map[string]*Day{}
	}
	if h.Foods == nil {
		h.Foods = map[string]*Food{}
	}
	if h.Health == nil {
		h.Health = map[string]*HealthDay{}
	}
	if h.OFF == nil {
		h.OFF = map[string]*OFFProduct{}
	}
	return h, nil
}

// Save writes atomically so an interrupted run never corrupts the history.
func (h *History) Save(path string) error {
	return WriteJSON(path, h, false)
}

func WriteJSON(path string, v any, indent bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var data []byte
	var err error
	if indent {
		data, err = json.MarshalIndent(v, "", "  ")
	} else {
		data, err = json.Marshal(v)
	}
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
