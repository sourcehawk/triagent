import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ClusterIdInput } from "./ClusterIdInput";
import { api } from "@/lib/api";

beforeEach(() => {
  vi.restoreAllMocks();
});

const schema = {
  id: "cluster_id",
  label: "Cluster",
  type: "cluster_id" as const,
  optional: true,
  placeholder: "abc-id",
  hint: "Namespace: {{.value}}-zeebe",
};

describe("ClusterIdInput", () => {
  it("renders the label and text input", () => {
    render(
      <ClusterIdInput
        schema={schema}
        value={{ value: "" }}
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByText("Cluster")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("abc-id")).toBeInTheDocument();
  });

  it("fires onChange when the text input changes", () => {
    const onChange = vi.fn();
    render(
      <ClusterIdInput
        schema={schema}
        value={{ value: "" }}
        onChange={onChange}
      />,
    );
    const input = screen.getByPlaceholderText("abc-id");
    fireEvent.change(input, { target: { value: "abc" } });
    expect(onChange).toHaveBeenCalledWith({ value: "abc" });
  });

  it("interpolates hint with current value", () => {
    render(
      <ClusterIdInput
        schema={schema}
        value={{ value: "abc" }}
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByText("Namespace: abc-zeebe")).toBeInTheDocument();
  });

  it("lazy-loads clusters only when the picker is opened", async () => {
    const spy = vi.spyOn(api, "listClusters").mockResolvedValue([]);
    render(
      <ClusterIdInput
        schema={schema}
        value={{ value: "" }}
        onChange={vi.fn()}
      />,
    );
    expect(spy).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /Pick a cluster/ }));
    await waitFor(() => expect(spy).toHaveBeenCalledOnce());
  });
});
