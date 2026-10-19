// Label colours (contracts/collab.md "Labels and colours"): the opendesk tones a Gmail
// label or an Outlook category takes. Gmail accepts only its own palette and Outlook its
// presets, so each tone is the colour that provider keeps.

export const LABEL_TONES = ["gray", "red", "orange", "amber", "green", "teal", "blue", "indigo", "violet", "pink"] as const;
export type LabelTone = (typeof LABEL_TONES)[number];

/** Gmail palette backgrounds, by tone. */
const GMAIL: Record<LabelTone, string> = {
  gray: "#999999",
  red: "#fb4c2f",
  orange: "#ffad47",
  amber: "#fad165",
  green: "#16a766",
  teal: "#2da2bb",
  blue: "#4a86e8",
  indigo: "#285bac",
  violet: "#a479e2",
  pink: "#f691b3",
};

/** Outlook category presets, by tone. */
const OUTLOOK: Record<LabelTone, string> = {
  gray: "#a0aeb2",
  red: "#e74856",
  orange: "#f7630c",
  amber: "#ffb900",
  green: "#13a10e",
  teal: "#00b294",
  blue: "#0078d4",
  indigo: "#004e8c",
  violet: "#8764b8",
  pink: "#c30052",
};

/** The colour a provider keeps for a tone. */
export function toneHex(provider: string, tone: LabelTone): string {
  return provider === "microsoft" ? OUTLOOK[tone] : GMAIL[tone];
}

/** The tone a stored colour is, when it is one of them. */
export function toneOf(provider: string, color: string | null): LabelTone | null {
  if (!color) return null;
  const c = color.toLowerCase();
  return LABEL_TONES.find((t) => toneHex(provider, t) === c) ?? null;
}
