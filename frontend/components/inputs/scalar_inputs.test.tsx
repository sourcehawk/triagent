import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { TextInput } from "./TextInput";
import { UrlInput } from "./UrlInput";
import { TextareaInput } from "./TextareaInput";

const baseSchema = {
  id: "x",
  label: "X",
  type: "text" as const,
  optional: true,
};

describe("TextInput", () => {
  it("renders label and fires onChange", () => {
    const onChange = vi.fn();
    render(
      <TextInput
        schema={{ ...baseSchema, placeholder: "p" }}
        value={{ value: "" }}
        onChange={onChange}
      />,
    );
    expect(screen.getByText("X")).toBeInTheDocument();
    const input = screen.getByPlaceholderText("p");
    fireEvent.change(input, { target: { value: "hello" } });
    expect(onChange).toHaveBeenCalledWith({ value: "hello" });
  });

  it("interpolates hint with current value", () => {
    render(
      <TextInput
        schema={{ ...baseSchema, hint: "Namespace: {{.value}}-zeebe" }}
        value={{ value: "abc" }}
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByText(/Namespace: abc-zeebe/)).toBeInTheDocument();
  });

  it("renders no hint paragraph when the template resolves to empty string", () => {
    const { container } = render(
      <TextInput
        schema={{ ...baseSchema, hint: "{{.missing}}" }}
        value={{ value: "abc" }}
        onChange={vi.fn()}
      />,
    );
    // {{.missing}} resolves to "" (missing key), so the full hint is ""
    // which is falsy — the <p> should not be rendered at all.
    expect(container.querySelector("p")).toBeNull();
  });
});

describe("UrlInput", () => {
  it("uses type=url", () => {
    const { container } = render(
      <UrlInput
        schema={{ ...baseSchema, type: "url" }}
        value={{ value: "https://x" }}
        onChange={vi.fn()}
      />,
    );
    const input = container.querySelector("input");
    expect(input?.type).toBe("url");
  });
});

describe("TextareaInput", () => {
  it("renders a textarea, not an input", () => {
    const { container } = render(
      <TextareaInput
        schema={{ ...baseSchema, type: "textarea" }}
        value={{ value: "" }}
        onChange={vi.fn()}
      />,
    );
    expect(container.querySelector("textarea")).not.toBeNull();
    expect(container.querySelector("input")).toBeNull();
  });

  it("fires onChange with the new value", () => {
    const onChange = vi.fn();
    render(
      <TextareaInput
        schema={{ ...baseSchema, type: "textarea" }}
        value={{ value: "" }}
        onChange={onChange}
      />,
    );
    const ta = screen.getByRole("textbox");
    fireEvent.change(ta, { target: { value: "multi\nline" } });
    expect(onChange).toHaveBeenCalledWith({ value: "multi\nline" });
  });
});
