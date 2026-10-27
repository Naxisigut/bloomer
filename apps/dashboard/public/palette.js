// Shared series palette. Twelve hues keep every model visually distinct up to the
// maximum number of models one analysis query may request.
// `#f2994a`/`#c08a4f` and `#5b8def`/`#8b7cf6` are the closest existing pairs
// (ΔE76 21–23); the four appended hues stay above ΔE 30 against every earlier entry
// and against the reserved reward grey, so adding them never creates a new near-pair.
export const MODEL_COLORS = [
  '#5b8def', '#f2994a', '#8b7cf6', '#2bb3a3', '#e5677e', '#9ab843', '#c08a4f', '#d4a72c',
  '#16a34a', '#a21caf', '#ef4444', '#0e7490',
];

// Reward payouts are not models, so they always draw in a neutral grey.
export const REWARD_COLOR = '#94a3b8';

// The home chart draws "全部收益" in blue, so model series start after it.
export const TOTAL_COLOR = '#5b8def';
export const HOME_MODEL_COLORS = MODEL_COLORS.filter(color => color !== TOTAL_COLOR);

export const MAX_MODELS = MODEL_COLORS.length;
