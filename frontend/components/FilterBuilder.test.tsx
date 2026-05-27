import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { FilterBuilder } from "./FilterBuilder";

describe("FilterBuilder", () => {
  it("adds and removes filter rows", () => {
    const onChange = vi.fn();
    render(<FilterBuilder kind="github_issues" value={[]} onChange={onChange} />);
    fireEvent.click(screen.getByText(/add filter/i));
    expect(onChange).toHaveBeenLastCalledWith([{ field: "title", op: "contains", value: "" }]);
  });
  it("limits fields to the per-kind allowed set", () => {
    render(<FilterBuilder kind="slack_channel" value={[{ field: "text", op: "contains", value: "x" }]} onChange={vi.fn()} />);
    const fieldSelect = screen.getAllByRole("combobox")[0];
    const options = Array.from(fieldSelect.querySelectorAll("option")).map(o => o.value);
    expect(options).toEqual(expect.arrayContaining(["text", "author"]));
    expect(options).not.toContain("title");
  });
});
