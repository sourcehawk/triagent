import type { InputProps, ScalarInputValue } from "./types";
import { interpolateHint } from "./types";

export function TextInput({ schema, value, onChange }: InputProps<ScalarInputValue>) {
  const hint = schema.hint ? interpolateHint(schema.hint, value) : "";
  return (
    <label className="block">
      <span className="mb-1 block text-xs uppercase tracking-wide text-zinc-400">
        {schema.label}
      </span>
      <input
        type="text"
        value={value.value}
        onChange={(e) => onChange({ value: e.target.value })}
        placeholder={schema.placeholder}
        className="w-full rounded border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 placeholder-zinc-600 focus:border-zinc-600 focus:outline-none"
      />
      {hint && <p className="mt-1 whitespace-pre-line text-xs text-zinc-500">{hint}</p>}
    </label>
  );
}
