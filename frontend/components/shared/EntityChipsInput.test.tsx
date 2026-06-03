import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { EntityChipsInput } from "@/components/shared/EntityChipsInput";

describe("EntityChipsInput", () => {
  it("adds a valid entity on Enter and clears the input", () => {
    const onChange = vi.fn();
    render(<EntityChipsInput label="services" values={[]} onChange={onChange} />);
    const input = screen.getByPlaceholderText("add entity…") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "zeebe-broker" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith(["zeebe-broker"]);
  });

  it("rejects malformed names and shows an error", () => {
    const onChange = vi.fn();
    render(<EntityChipsInput label="services" values={[]} onChange={onChange} />);
    const input = screen.getByPlaceholderText("add entity…") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Zeebe Broker" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText(/must be lowercase \+ hyphens/)).toBeTruthy();
  });

  it("rejects duplicates", () => {
    const onChange = vi.fn();
    render(<EntityChipsInput label="services" values={["zeebe-broker"]} onChange={onChange} />);
    const input = screen.getByPlaceholderText("add entity…") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "zeebe-broker" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText(/already in the list/)).toBeTruthy();
  });

  it("removes an entity when the × button is clicked", () => {
    const onChange = vi.fn();
    render(<EntityChipsInput label="services" values={["a", "b"]} onChange={onChange} />);
    fireEvent.click(screen.getByLabelText("Remove a"));
    expect(onChange).toHaveBeenCalledWith(["b"]);
  });
});
