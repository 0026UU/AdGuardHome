/**
 * Palette for everything Chart.js paints.
 *
 * Chart.js draws straight onto a `<canvas>`: the 2D context receives plain
 * color strings and never resolves CSS custom properties, so `var(--...)`
 * cannot be used here. This module is therefore the single place where the
 * canvas colors live, and every value names the semantic token it mirrors —
 * when a token is retuned, the constant beside it is the one to update.
 *
 * The mirrored tokens all come from the Apple system ramps
 * (`common/styles/colors/adg.css`). Only the brand ramp is swapped per theme
 * (`--product-primary-50` = #007AFF light / #0A84FF dark), and nothing below is
 * brand-based, so each hex is simultaneously the light and the dark value:
 * `--red-50`, `--orange-50`, `--product-secondary-50` and `--gray-60` keep
 * these exact values under `[data-theme='dark']`.
 */

/** The four stat-card series, in dashboard order. */
export const CHART_COLORS = {
    /** `--default-tertiary-stat` / `--gray-60` — neutral total-queries baseline */
    QUERIES: '#8E8E93',
    /** `--default-error-link` / `--red-50` — Apple systemRed, blocked by filters */
    ADS: '#FF3B30',
    /** `--default-attention-link` / `--orange-50` — Apple systemOrange, threats */
    THREATS: '#FF9F0A',
    /** `--default-additional-link` / `--product-secondary-50` — systemPurple, adult */
    ADULT: '#AF52DE',
} as const;

/**
 * Alpha suffixes for the area fill under a line, appended to a 6-digit hex.
 * 30 % at the line fading to fully transparent at the axis, which is how Apple
 * charts keep a filled series from competing with the line itself.
 */
export const CHART_AREA_GRADIENT = {
    TOP: '4D',
    BOTTOM: '00',
} as const;
