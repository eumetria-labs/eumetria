package store

// FoodOf is the product or recipe version an entry was eaten as, nil when
// it isn't known: simple and AI entries, and product entries synced before
// DayVersion 7 (they have no ProductID yet).
func (h *History) FoodOf(it Item) *Food {
	switch {
	case it.ProductID != "":
		return h.Foods["product:"+it.ProductID]
	case it.RecipeID != "":
		if f := h.Foods[RecipeKey(it.RecipeID, it.RecipeVersion)]; f != nil {
			return f
		}
		return h.Foods["recipe:"+it.RecipeID]
	}
	return nil
}

// Scale turns a food's nutrients (per g, ml or portion) into the entry's:
// by kcal, which also holds for a recipe whose kcal were scaled because its
// version is unknown (RecipeStale); by amount for a food without kcal
// (water, black coffee).
func Scale(it Item, f *Food) float64 {
	if e := f.Nutrients["energy.energy"]; e > 0 && it.Energy > 0 {
		return it.Energy / e
	}
	return it.Amount
}

// SodiumPerSalt is the share of sodium in table salt: the sodium of a food
// that lists only its salt.
const SodiumPerSalt = 0.3934

// Nutrient is a food's amount of one nutrient (in grams, per g, ml or
// portion, as Yazio has it) and whether the food lists it. Sodium comes
// from the salt when only that is listed.
func (f *Food) Nutrient(key string) (float64, bool) {
	if v, ok := f.Nutrients[key]; ok {
		return v, true
	}
	if key == "nutrient.sodium" {
		if salt, ok := f.Nutrients["nutrient.salt"]; ok {
			return salt * SodiumPerSalt, true
		}
	}
	return 0, false
}
