"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  Background,
  Controls,
  ReactFlow,
  ReactFlowProvider,
  type Edge,
  type Node,
  useEdgesState,
  useNodesState,
  useReactFlow,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import dagre from "dagre";
import { useRouter } from "next/navigation";
import { wikiApi } from "@/lib/wiki-api";
import type { WikiEntryDetail, WikiEntityDetail } from "@/lib/wiki-api";
import { useClickToFocus } from "@/lib/use-click-to-focus";
import {
  IncidentNodeView,
  EntityNodeView,
  PlaceholderNodeView,
  INCIDENT_NODE_WIDTH,
  INCIDENT_NODE_HEIGHT,
  ENTITY_NODE_WIDTH,
  ENTITY_NODE_HEIGHT,
} from "./WikiGraphNodes";
import type {
  IncidentNodeData,
  EntityNodeData,
  PlaceholderNodeData,
} from "./WikiGraphNodes";

// ── Types ────────────────────────────────────────────────────────────────────

// NodeKey uniquely identifies a graph node:
//   "incident:<id>"              e.g. "incident:INC-12345-foo"
//   "entity:<type>:<name>"       e.g. "entity:service:zeebe-broker"
type NodeKey = string;

function incidentKey(id: string): NodeKey {
  return `incident:${id}`;
}

function entityKey(type: string, name: string): NodeKey {
  return `entity:${type}:${name}`;
}

// ── nodeTypes map — stable reference required by xyflow ─────────────────────

const nodeTypes = {
  incident: IncidentNodeView,
  entity: EntityNodeView,
  placeholder: PlaceholderNodeView,
};

// ── Public props ─────────────────────────────────────────────────────────────

// Selection identifies a node in the graph by kind. Mirrors the
// editor's Selection union and is what onSelect emits.
export type WikiGraphSelection =
  | { kind: "incident"; id: string }
  | { kind: "entity"; type: string; name: string };

type SharedWikiGraphProps = {
  // When set, clicking a graph node calls onSelect with the node's
  // identity instead of routing to its dedicated page. Drives the
  // editor surface's "select-to-edit" flow. Read-only callers leave
  // this unset and the graph keeps its previous navigate-on-click
  // behaviour.
  onSelect?: (sel: WikiGraphSelection) => void;
  // selectedKey is the NodeKey of the currently-selected node. The
  // matching node renders a sky-coloured ring. Format mirrors the
  // graph's internal key encoding.
  selectedKey?: string;
  // dirtyKeys is the set of NodeKeys whose drafts are dirty in the
  // editor. Matching nodes render a small amber dot.
  dirtyKeys?: Set<string>;
};

export type WikiGraphProps = SharedWikiGraphProps &
  (
    | { rootKind: "incident"; rootId: string }
    | { rootKind: "entity"; rootType: string; rootName: string }
  );

// ── Outer wrapper (ReactFlowProvider) ────────────────────────────────────────

export function WikiGraph(props: WikiGraphProps) {
  return (
    <ReactFlowProvider>
      <WikiGraphInner {...props} />
    </ReactFlowProvider>
  );
}

// ── Layout ───────────────────────────────────────────────────────────────────

function applyDagreLayout(nodes: Node[], edges: Edge[]): Node[] {
  if (nodes.length === 0) return nodes;

  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({
    rankdir: "TB",
    nodesep: 60,
    ranksep: 80,
    marginx: 24,
    marginy: 24,
  });

  for (const n of nodes) {
    const isIncident =
      (n.data as Record<string, unknown>).kind === "incident";
    g.setNode(n.id, {
      width: isIncident ? INCIDENT_NODE_WIDTH : ENTITY_NODE_WIDTH,
      height: isIncident ? INCIDENT_NODE_HEIGHT : ENTITY_NODE_HEIGHT,
    });
  }

  const nodeIds = new Set(nodes.map((n) => n.id));
  for (const e of edges) {
    // Only add edge if both endpoints are in the graph.
    if (nodeIds.has(e.source) && nodeIds.has(e.target)) {
      g.setEdge(e.source, e.target);
    }
  }

  dagre.layout(g);

  return nodes.map((n) => {
    const pos = g.node(n.id);
    const isIncident =
      (n.data as Record<string, unknown>).kind === "incident";
    const w = isIncident ? INCIDENT_NODE_WIDTH : ENTITY_NODE_WIDTH;
    const h = isIncident ? INCIDENT_NODE_HEIGHT : ENTITY_NODE_HEIGHT;
    return {
      ...n,
      position: {
        x: (pos?.x ?? 0) - w / 2,
        y: (pos?.y ?? 0) - h / 2,
      },
    };
  });
}

// ── Inner component ──────────────────────────────────────────────────────────

function WikiGraphInner(props: WikiGraphProps) {
  const router = useRouter();
  const reactFlow = useReactFlow();
  const { onSelect, selectedKey, dirtyKeys } = props;

  // fitViewSoon waits for two animation frames before calling fitView.
  // queueMicrotask runs before the browser has flushed the React-driven
  // layout, so the previous implementation measured against stale
  // container dimensions and produced an off-centre transform that
  // looked like "the graph is centred to the page, not the panel".
  // Two RAFs is the canonical "let the browser paint, then measure"
  // pattern and gets the right rect on first mount + tab switches.
  const fitViewSoon = useCallback(
    (duration = 200) => {
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          try {
            reactFlow.fitView({ padding: 0.25, duration });
          } catch {
            /* fitView throws if the container is detached — non-fatal */
          }
        });
      });
    },
    [reactFlow],
  );

  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);

  // Track which nodes are expanded (have had their neighbours fetched).
  const [expandedKeys, setExpandedKeys] = useState<Set<NodeKey>>(new Set());
  // Track which nodes are currently loading.
  const [loadingKeys, setLoadingKeys] = useState<Set<NodeKey>>(new Set());

  // Stable refs so async expand callbacks see current state without stale closures.
  const expandedRef = useRef<Set<NodeKey>>(new Set());
  const loadingRef = useRef<Set<NodeKey>>(new Set());
  const nodesRef = useRef<Node[]>([]);
  const edgesRef = useRef<Edge[]>([]);

  // Keep refs in sync with state.
  useEffect(() => { expandedRef.current = expandedKeys; }, [expandedKeys]);
  useEffect(() => { loadingRef.current = loadingKeys; }, [loadingKeys]);
  useEffect(() => { nodesRef.current = nodes; }, [nodes]);
  useEffect(() => { edgesRef.current = edges; }, [edges]);

  // Cache of fetched data so we don't re-fetch on re-render.
  const incidentCache = useRef<Map<string, WikiEntryDetail>>(new Map());
  const entityCache = useRef<Map<string, WikiEntityDetail>>(new Map());

  // entityCounts: total backlinks per entity ("<type>:<name>" → N).
  // Fetched once on mount via wikiApi.listEntities so the entity nodes
  // know whether expanding them would reveal new neighbours. An entity
  // referenced only by its current parent in the graph has 0
  // prospective children — we hide its expand chevron entirely.
  const entityCountsRef = useRef<Map<string, number>>(new Map());
  // Bumped each time entityCountsRef updates so the data-sync effect
  // can reflow chevrons on already-rendered nodes.
  const [entityCountsVersion, setEntityCountsVersion] = useState(0);

  useEffect(() => {
    let cancelled = false;
    wikiApi
      .listEntities()
      .then((res) => {
        if (cancelled) return;
        const m = new Map<string, number>();
        for (const e of res.entities) {
          m.set(`${e.type}:${e.name}`, e.entry_count);
        }
        entityCountsRef.current = m;
        setEntityCountsVersion((v) => v + 1);
      })
      .catch(() => {
        /* silent — chevron-show fallback is the existing behaviour */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // prospectiveChildrenForEntity returns how many incidents would be
  // *newly* added by expanding this entity — its total backlinks minus
  // the incidents it's already connected to in the graph. Returns
  // undefined when we don't have the count yet (still rendered as a
  // chevron-only badge as a fallback so the operator can still try).
  function prospectiveChildrenForEntity(
    type: string,
    name: string,
    edges: Edge[],
  ): number | undefined {
    const total = entityCountsRef.current.get(`${type}:${name}`);
    if (total === undefined) return undefined;
    const eKey = entityKey(type, name);
    let connected = 0;
    for (const e of edges) {
      if (e.source === eKey && e.target.startsWith("incident:")) connected++;
      else if (e.target === eKey && e.source.startsWith("incident:")) connected++;
    }
    return Math.max(0, total - connected);
  }

  // ── Root key helper ───────────────────────────────────────────────────────

  function getRootKey(): NodeKey {
    if (props.rootKind === "incident") return incidentKey(props.rootId);
    return entityKey(props.rootType, props.rootName);
  }

  // ── Node factory helpers ──────────────────────────────────────────────────

  function makeIncidentNode(
    id: string,
    title: string,
    severity: string | undefined,
    status: string | undefined,
    rootKey: NodeKey,
    overrides?: Partial<IncidentNodeData>,
  ): Node {
    const key = incidentKey(id);
    const data: IncidentNodeData = {
      kind: "incident",
      incidentId: id,
      title,
      severity,
      status,
      isRoot: key === rootKey,
      expanded: expandedRef.current.has(key),
      loading: loadingRef.current.has(key),
      selected: selectedKey === key,
      dirty: dirtyKeys?.has(key) ?? false,
      onExpand: handleExpand,
      onNavigate: handleNavigate,
      onSelect: onSelect ? handleSelect : undefined,
      onCollapse: onSelect ? handleCollapse : undefined,
      ...overrides,
    };
    return {
      id: key,
      type: "incident",
      data: data as unknown as Record<string, unknown>,
      position: { x: 0, y: 0 },
    };
  }

  function makeEntityNode(
    type: string,
    name: string,
    rootKey: NodeKey,
    overrides?: Partial<EntityNodeData>,
  ): Node {
    const key = entityKey(type, name);
    const data: EntityNodeData = {
      kind: "entity",
      entityType: type,
      entityName: name,
      isRoot: key === rootKey,
      expanded: expandedRef.current.has(key),
      loading: loadingRef.current.has(key),
      selected: selectedKey === key,
      dirty: dirtyKeys?.has(key) ?? false,
      prospectiveChildren: prospectiveChildrenForEntity(
        type,
        name,
        edgesRef.current,
      ),
      onExpand: handleExpand,
      onNavigate: handleNavigate,
      onSelect: onSelect ? handleSelect : undefined,
      onCollapse: onSelect ? handleCollapse : undefined,
      ...overrides,
    };
    return {
      id: key,
      type: "entity",
      data: data as unknown as Record<string, unknown>,
      position: { x: 0, y: 0 },
    };
  }

  function makePlaceholderNode(label: string, forKey: NodeKey): Node {
    const data: PlaceholderNodeData = { kind: "placeholder", label };
    return {
      id: `placeholder:${forKey}`,
      type: "placeholder",
      data: data as unknown as Record<string, unknown>,
      position: { x: 0, y: 0 },
    };
  }
  // Suppress unused warning — placeholder is available for future use.
  void makePlaceholderNode;

  function makeEdge(sourceKey: NodeKey, targetKey: NodeKey): Edge {
    return {
      id: `${sourceKey}__${targetKey}`,
      source: sourceKey,
      target: targetKey,
      style: { stroke: "#3f3f46" },
    };
  }

  // ── Handlers threaded into node data ──────────────────────────────────────

  const handleNavigate = useCallback(
    (key: NodeKey) => {
      const parts = key.split(":");
      if (parts[0] === "incident") {
        const id = parts.slice(1).join(":");
        router.push(`/wiki/entries/?slug=${encodeURIComponent(id)}`);
      } else if (parts[0] === "entity") {
        const type = parts[1];
        const name = parts.slice(2).join(":");
        router.push(
          `/wiki/entities/?type=${encodeURIComponent(type)}&name=${encodeURIComponent(name)}`,
        );
      }
    },
    [router],
  );

  const handleSelect = useCallback(
    (key: NodeKey) => {
      if (!onSelect) return;
      const parts = key.split(":");
      if (parts[0] === "incident") {
        onSelect({ kind: "incident", id: parts.slice(1).join(":") });
      } else if (parts[0] === "entity") {
        onSelect({
          kind: "entity",
          type: parts[1],
          name: parts.slice(2).join(":"),
        });
      }
    },
    [onSelect],
  );

  // handleCollapse undoes a node's expansion. Children that became
  // visible because of this node — i.e. nodes whose only path back to
  // the root runs through this one — are pruned. Children also
  // reachable from another expanded node stay; the edge from this
  // node to them stays too. Implemented as a BFS from the root over
  // the graph minus all edges incident to the collapsed node — any
  // node not reached that way was "only here because of this one".
  const handleCollapse = useCallback(
    (key: NodeKey) => {
      const rootKey = getRootKey();
      if (key === rootKey) return; // can't collapse the root

      const adj = new Map<NodeKey, NodeKey[]>();
      for (const e of edgesRef.current) {
        if (e.source === key || e.target === key) continue;
        const a = adj.get(e.source) ?? [];
        a.push(e.target);
        adj.set(e.source, a);
        const b = adj.get(e.target) ?? [];
        b.push(e.source);
        adj.set(e.target, b);
      }

      const reachable = new Set<NodeKey>([rootKey]);
      const queue: NodeKey[] = [rootKey];
      while (queue.length) {
        const cur = queue.shift()!;
        for (const next of adj.get(cur) ?? []) {
          if (!reachable.has(next)) {
            reachable.add(next);
            queue.push(next);
          }
        }
      }
      // Keep the collapsed node itself — it's still in the graph,
      // just back to its un-expanded state.
      reachable.add(key);

      const newEdges = edgesRef.current.filter(
        (e) => reachable.has(e.source) && reachable.has(e.target),
      );
      const newNodes = nodesRef.current.filter((n) => reachable.has(n.id));

      setNodes(applyDagreLayout(newNodes, newEdges));
      setEdges(newEdges);
      setExpandedKeys((prev) => {
        const next = new Set(prev);
        next.delete(key);
        for (const k of prev) {
          if (!reachable.has(k)) next.delete(k);
        }
        return next;
      });
      setLoadingKeys((prev) => {
        const next = new Set(prev);
        for (const k of prev) {
          if (!reachable.has(k)) next.delete(k);
        }
        return next;
      });
      fitViewSoon();
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [fitViewSoon],
  );

  const handleExpand = useCallback(
    async (key: NodeKey) => {
      if (loadingRef.current.has(key) || expandedRef.current.has(key)) return;

      // Mark loading.
      setLoadingKeys((prev) => new Set(prev).add(key));

      const rootKey = getRootKey();

      try {
        const parts = key.split(":");
        if (parts[0] === "incident") {
          const id = parts.slice(1).join(":");
          let detail = incidentCache.current.get(id);
          if (!detail) {
            detail = await wikiApi.getEntry(id);
            incidentCache.current.set(id, detail);
          }

          const fm = detail.frontmatter;
          const entityRefs: Array<{ type: string; name: string }> = [
            ...(fm.services ?? []).map((n) => ({ type: "service", name: n })),
            ...(fm.errors ?? []).map((n) => ({ type: "error", name: n })),
            ...(fm.symptoms ?? []).map((n) => ({ type: "symptom", name: n })),
          ];

          // Bidirectional connectivity check — an edge in either direction
          // counts as "already connected" so we don't draw a redundant
          // back-edge when an expanded child references its parent.
          const alreadyConnected = (s: string, t: string) =>
            edgesRef.current.some(
              (e) =>
                (e.source === s && e.target === t) ||
                (e.source === t && e.target === s),
            );

          // Build the new edges first so dagre can use them in the layout pass.
          const newIncEdges: Edge[] = entityRefs
            .map((e) => makeEdge(key, entityKey(e.type, e.name)))
            .filter((edge) => !alreadyConnected(edge.source, edge.target));
          const allEdges = [...edgesRef.current, ...newIncEdges];

          // Count of *new* children added by this expansion — surfaced on
          // the node so the operator can see at a glance how many branches
          // came in. A 0-result expansion is recorded too: the node visibly
          // flips to "expanded with no children" so the user knows clicking
          // again won't help.
          const addedChildCount = entityRefs.length;

          setNodes((prevNodes) => {
            const existingIds = new Set(prevNodes.map((n) => n.id));
            const additions: Node[] = [];
            for (const e of entityRefs) {
              const eKey = entityKey(e.type, e.name);
              if (!existingIds.has(eKey)) {
                additions.push(makeEntityNode(e.type, e.name, rootKey));
              }
            }
            const updated = prevNodes.map((n) =>
              n.id === key
                ? {
                    ...n,
                    data: {
                      ...(n.data as Record<string, unknown>),
                      title: fm.title,
                      severity: fm.severity,
                      status: fm.status,
                      expanded: true,
                      loading: false,
                      childCount: addedChildCount,
                    },
                  }
                : n,
            );
            return applyDagreLayout([...updated, ...additions], allEdges);
          });
          setEdges(allEdges);
          fitViewSoon();
        } else if (parts[0] === "entity") {
          const type = parts[1];
          const name = parts.slice(2).join(":");
          const cacheKey = `${type}:${name}`;
          let detail = entityCache.current.get(cacheKey);
          if (!detail) {
            detail = await wikiApi.getEntity(type, name);
            entityCache.current.set(cacheKey, detail);
          }

          const backlinks = detail.backlinks ?? [];

          const alreadyConnectedEnt = (s: string, t: string) =>
            edgesRef.current.some(
              (e) =>
                (e.source === s && e.target === t) ||
                (e.source === t && e.target === s),
            );

          const newEntEdges: Edge[] = backlinks
            .map((bl) => makeEdge(key, incidentKey(bl.id)))
            .filter((edge) => !alreadyConnectedEnt(edge.source, edge.target));
          const allEntEdges = [...edgesRef.current, ...newEntEdges];

          const addedEntChildCount = backlinks.length;

          setNodes((prevNodes) => {
            const existingIds = new Set(prevNodes.map((n) => n.id));
            const additions: Node[] = [];
            for (const bl of backlinks) {
              const iKey = incidentKey(bl.id);
              if (!existingIds.has(iKey)) {
                additions.push(
                  makeIncidentNode(bl.id, bl.title, bl.severity, bl.status, rootKey),
                );
              }
            }
            const updated = prevNodes.map((n) =>
              n.id === key
                ? {
                    ...n,
                    data: {
                      ...(n.data as Record<string, unknown>),
                      expanded: true,
                      loading: false,
                      childCount: addedEntChildCount,
                    },
                  }
                : n,
            );
            return applyDagreLayout([...updated, ...additions], allEntEdges);
          });
          setEdges(allEntEdges);
          fitViewSoon();
        }
      } catch {
        // Silently mark expanded-but-failed so we don't loop.
        setNodes((prev) =>
          prev.map((n) =>
            n.id === key
              ? {
                  ...n,
                  data: {
                    ...(n.data as Record<string, unknown>),
                    loading: false,
                  },
                }
              : n,
          ),
        );
      }

      setExpandedKeys((prev) => new Set(prev).add(key));
      setLoadingKeys((prev) => {
        const next = new Set(prev);
        next.delete(key);
        return next;
      });
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [props.rootKind, reactFlow],
  );

  // Re-render badge/spinner state when expanded/loading sets change.
  // Only update the data fields — no dagre re-layout needed since positions
  // are already correct from the expand operation that triggered this.
  useEffect(() => {
    setNodes((prev) =>
      prev.map((n) => {
        const d = n.data as Record<string, unknown>;
        if (d.kind === "incident" || d.kind === "entity") {
          const wasExpanded = d.expanded as boolean;
          const wasLoading = d.loading as boolean;
          const isExpanded = expandedKeys.has(n.id);
          const isLoading = loadingKeys.has(n.id);
          if (wasExpanded === isExpanded && wasLoading === isLoading) return n;
          return {
            ...n,
            data: { ...d, expanded: isExpanded, loading: isLoading },
          };
        }
        return n;
      }),
    );
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expandedKeys, loadingKeys]);

  // Sync selection + dirty data fields without re-laying out. Selecting
  // a different node shouldn't shuffle the graph, just retoggle the
  // ring + amber dot.
  useEffect(() => {
    setNodes((prev) =>
      prev.map((n) => {
        const d = n.data as Record<string, unknown>;
        if (d.kind === "incident" || d.kind === "entity") {
          const isSelected = selectedKey === n.id;
          const isDirty = dirtyKeys?.has(n.id) ?? false;
          if (d.selected === isSelected && d.dirty === isDirty) return n;
          return {
            ...n,
            data: { ...d, selected: isSelected, dirty: isDirty },
          };
        }
        return n;
      }),
    );
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedKey, dirtyKeys]);

  // Recompute entity prospective-children when the count map first
  // loads or when edges change (new connections reduce prospective
  // count). Touches data only — no dagre reflow.
  useEffect(() => {
    setNodes((prev) =>
      prev.map((n) => {
        const d = n.data as Record<string, unknown>;
        if (d.kind !== "entity") return n;
        const next = prospectiveChildrenForEntity(
          d.entityType as string,
          d.entityName as string,
          edgesRef.current,
        );
        if (d.prospectiveChildren === next) return n;
        return { ...n, data: { ...d, prospectiveChildren: next } };
      }),
    );
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [entityCountsVersion, edges]);

  // ── Initial seed: bootstrap the graph on mount ────────────────────────────

  useEffect(() => {
    let cancelled = false;

    async function init() {
      const rootKey = getRootKey();

      if (props.rootKind === "incident") {
        // Show root node immediately so the graph is not empty.
        const rootNode = makeIncidentNode(
          props.rootId,
          props.rootId,
          undefined,
          undefined,
          rootKey,
          { loading: true },
        );
        if (!cancelled) {
          setNodes([rootNode]);
          setEdges([]);
        }

        try {
          const detail = await wikiApi.getEntry(props.rootId);
          incidentCache.current.set(props.rootId, detail);
          if (cancelled) return;

          const fm = detail.frontmatter;
          const entityRefs: Array<{ type: string; name: string }> = [
            ...(fm.services ?? []).map((n) => ({ type: "service", name: n })),
            ...(fm.errors ?? []).map((n) => ({ type: "error", name: n })),
            ...(fm.symptoms ?? []).map((n) => ({ type: "symptom", name: n })),
          ];

          const newNodes: Node[] = [
            makeIncidentNode(props.rootId, fm.title, fm.severity, fm.status, rootKey, {
              expanded: true,
              loading: false,
            }),
          ];
          const newEdges: Edge[] = [];

          for (const e of entityRefs) {
            const eKey = entityKey(e.type, e.name);
            newNodes.push(makeEntityNode(e.type, e.name, rootKey));
            newEdges.push(makeEdge(rootKey, eKey));
          }

          setExpandedKeys(new Set([rootKey]));
          setNodes(applyDagreLayout(newNodes, newEdges));
          setEdges(newEdges);
          // Re-fit after the seed layout so the graph is centred on mount
          // (fitView on <ReactFlow> only runs with the initial empty/root node).
          fitViewSoon();
        } catch {
          // Leave the root node visible, just unmark loading.
          if (!cancelled) {
            setNodes((prev) =>
              prev.map((n) =>
                n.id === rootKey
                  ? { ...n, data: { ...(n.data as Record<string, unknown>), loading: false } }
                  : n,
              ),
            );
          }
        }
      } else {
        // Entity root.
        const rootNode = makeEntityNode(
          props.rootType,
          props.rootName,
          rootKey,
          { loading: true },
        );
        if (!cancelled) {
          setNodes([rootNode]);
          setEdges([]);
        }

        try {
          const detail = await wikiApi.getEntity(props.rootType, props.rootName);
          entityCache.current.set(`${props.rootType}:${props.rootName}`, detail);
          if (cancelled) return;

          const backlinks = detail.backlinks ?? [];

          const newNodes: Node[] = [
            makeEntityNode(props.rootType, props.rootName, rootKey, {
              expanded: true,
              loading: false,
            }),
          ];
          const newEdges: Edge[] = [];

          for (const bl of backlinks) {
            const iKey = incidentKey(bl.id);
            newNodes.push(
              makeIncidentNode(bl.id, bl.title, bl.severity, bl.status, rootKey),
            );
            newEdges.push(makeEdge(rootKey, iKey));
          }

          setExpandedKeys(new Set([rootKey]));
          setNodes(applyDagreLayout(newNodes, newEdges));
          setEdges(newEdges);
          // Re-fit after the seed layout (entity root path).
          fitViewSoon();
        } catch {
          if (!cancelled) {
            setNodes((prev) =>
              prev.map((n) =>
                n.id === rootKey
                  ? { ...n, data: { ...(n.data as Record<string, unknown>), loading: false } }
                  : n,
              ),
            );
          }
        }
      }
    }

    init();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    props.rootKind,
    (props as { rootId?: string }).rootId,
    (props as { rootType?: string }).rootType,
    (props as { rootName?: string }).rootName,
  ]);

  // ── Render ────────────────────────────────────────────────────────────────

  // ResizeObserver triggers a re-fit whenever the wrapper's box
  // changes — covers tab switch (diagram tab gains size when its
  // sibling content unmounts), window resize, dev-tools open, panel
  // drag. Without this, the initial mount fit can be wrong if the
  // browser hasn't given the container its final size yet.
  const { ref: wrapperRef, focused, focus } =
    useClickToFocus<HTMLDivElement>();
  useEffect(() => {
    const el = wrapperRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => fitViewSoon(0));
    ro.observe(el);
    return () => ro.disconnect();
  }, [fitViewSoon, wrapperRef]);

  return (
    <div
      ref={wrapperRef}
      onPointerDown={focus}
      className="h-full w-full rounded border border-zinc-800 bg-zinc-950/60"
    >
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        nodeTypes={nodeTypes}
        fitView
        fitViewOptions={{ padding: 0.25 }}
        minZoom={0.1}
        maxZoom={2}
        zoomOnScroll={focused}
        zoomOnPinch={focused}
        proOptions={{ hideAttribution: true }}
      >
        <Background color="#27272a" gap={16} />
        <Controls position="bottom-right" showInteractive={false} />
      </ReactFlow>
    </div>
  );
}
