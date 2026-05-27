// Shared editor-toolbar button vocabulary. PlaybookEditor and
// WikiEditor render the same logical action row (discard / save /
// push-PR / chat) and need to read as a single control surface, not
// a grab-bag of pills the various features bolted on over time. All
// variants share the same shape (rounded border, px-3 py-1.5,
// text-xs, flex-row icon + label) — only the colour / weight changes
// per intent.

export const BTN_BASE =
  "inline-flex items-center gap-1.5 rounded border px-3 py-1.5 text-xs font-medium transition";

// Default: muted zinc, brightens on hover. Used for non-destructive,
// non-primary actions (discard, push-PR-when-available, chat-closed).
// disabled: rules come after hover: in source order so a disabled button
// does not pick up the bright hover border when the cursor passes over it.
export const BTN_SECONDARY =
  `${BTN_BASE} border-zinc-800 text-zinc-400 hover:border-zinc-600 hover:text-zinc-200 disabled:cursor-not-allowed disabled:border-zinc-900 disabled:text-zinc-600 disabled:hover:border-zinc-900 disabled:hover:text-zinc-600`;

// Sky-tinted "this control is currently engaged" state — only the
// chat toggle uses it, signalling that the drawer is open behind.
export const BTN_SECONDARY_ACTIVE =
  `${BTN_BASE} border-sky-700/60 bg-sky-950/40 text-sky-200 hover:bg-sky-950/60 disabled:cursor-not-allowed disabled:border-zinc-900 disabled:bg-transparent disabled:text-zinc-600 disabled:hover:bg-transparent`;

// Filled-light primary. Save is the one action that gets weight —
// it commits the operator's edits and is the expected "done" gesture.
//
// Disabled: keep the muted zinc-800 fill so the button is still
// visibly "the save" and not just a flat hole, but drop the border
// down to zinc-900 (matches BTN_SECONDARY / BTN_GATED disabled). The
// previous zinc-700 border read as a hard outline on the dark
// background, which the operator flagged as visually noisy.
export const BTN_PRIMARY =
  `${BTN_BASE} border-zinc-100 bg-zinc-100 text-zinc-900 hover:border-white hover:bg-white disabled:cursor-not-allowed disabled:border-zinc-900 disabled:bg-zinc-800 disabled:text-zinc-500`;

// Red-tinted destructive — reserved for delete-style actions.
export const BTN_DANGER =
  `${BTN_BASE} border-red-900/60 text-red-300 hover:bg-red-950/30 hover:text-red-200 disabled:cursor-not-allowed disabled:border-zinc-900 disabled:bg-transparent disabled:text-zinc-600 disabled:hover:bg-transparent disabled:hover:text-zinc-600`;

// Gated state for actions that exist but the operator can't take
// right now (gh missing, validation errors, …). Stays visible so
// the affordance is discoverable (icon + label remain rendered);
// tooltip explains why. Border is transparent — a 1px outline on
// a button the operator can't press read as visual noise next to
// the active controls in the same row.
export const BTN_GATED =
  `${BTN_BASE} cursor-not-allowed border-transparent text-zinc-600`;
