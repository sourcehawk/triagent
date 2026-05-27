import type { InputSchema } from "@/lib/api";

/**
 * Common props for every input-renderer component. The value's shape is
 * type-specific: scalar inputs (text/url/textarea/cluster_id) use
 * { value: string }; slack_channel uses { id: string; name: string; url: string }.
 */
export type InputProps<TValue> = {
  schema: InputSchema;
  value: TValue;
  onChange: (next: TValue) => void;
};

export type ScalarInputValue = { value: string };

/**
 * Minimal {{.field}} substitution for live-rendering input hints.
 * Profile authors use Go template syntax; this client-side helper handles
 * the simple field-expansion case ({{.value}}, {{.id}}, etc.). No control
 * flow (no {{if}}, {{range}}, etc.) — those belong server-side.
 */
export function interpolateHint(template: string, ctx: Record<string, unknown>): string {
  return template.replace(/{{\s*\.(\w+)\s*}}/g, (_m, key) => {
    const v = ctx[key];
    return v == null ? "" : String(v);
  });
}
