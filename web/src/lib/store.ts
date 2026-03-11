import { create } from "zustand";
import type { SSEEvent } from "./types";
import {
  uploadPcap,
  analyzeSession,
  getSession,
} from "./api";

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

interface AppState {
  // Session
  currentSessionId: string | null;
  events: SSEEvent[];
  sessionActive: boolean;
  loading: boolean;

  // UI chrome
  sidebarCollapsed: boolean;
  pcapManagerOpen: boolean;
  sidebarRefreshKey: number;

  // Internal – not used directly by components
  _abortController: AbortController | null;
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

interface AppActions {
  // Low-level
  setCurrentSessionId: (id: string | null) => void;
  appendEvent: (ev: SSEEvent) => void;
  setEvents: (evs: SSEEvent[]) => void;
  clearEvents: () => void;
  setSessionActive: (v: boolean) => void;
  setLoading: (v: boolean) => void;
  toggleSidebar: () => void;
  setPcapManagerOpen: (v: boolean) => void;
  bumpSidebarRefresh: () => void;

  // High-level workflows
  startStream: (sessionId: string, query: string) => void;
  handleSend: (query: string, file: File | null) => Promise<void>;
  handleCancel: () => void;
  handleSelectSession: (id: string) => Promise<void>;
  handleNewSession: () => void;
  handleAnalyzeFromManager: (sessionId: string) => void;
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

export const useStore = create<AppState & AppActions>()((set, get) => ({
  // --- initial state ---
  currentSessionId: null,
  events: [],
  sessionActive: false,
  loading: false,
  sidebarCollapsed: false,
  pcapManagerOpen: false,
  sidebarRefreshKey: 0,
  _abortController: null,

  // --- low-level setters ---
  setCurrentSessionId: (id) => set({ currentSessionId: id }),
  appendEvent: (ev) => set((s) => ({ events: [...s.events, ev] })),
  setEvents: (evs) => set({ events: evs }),
  clearEvents: () => set({ events: [] }),
  setSessionActive: (v) => set({ sessionActive: v }),
  setLoading: (v) => set({ loading: v }),
  toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
  setPcapManagerOpen: (v) => set({ pcapManagerOpen: v }),
  bumpSidebarRefresh: () =>
    set((s) => ({ sidebarRefreshKey: s.sidebarRefreshKey + 1 })),

  // --- high-level workflows ---

  startStream: (sessionId, query) => {
    const prev = get()._abortController;
    prev?.abort();

    set({ sessionActive: true });

    const controller = analyzeSession(
      sessionId,
      query,
      (ev) => get().appendEvent(ev),
      (_status) => {
        set({ sessionActive: false, _abortController: null });
        get().bumpSidebarRefresh();
      },
      (err) => {
        set({ sessionActive: false, _abortController: null });
        get().appendEvent({
          seq: 0,
          type: "error",
          data: { phase: "stream", message: err },
        });
        get().bumpSidebarRefresh();
      },
    );
    set({ _abortController: controller });
  },

  handleSend: async (query, file) => {
    set({ loading: true });
    try {
      const { currentSessionId, startStream, appendEvent } = get();

      if (currentSessionId && !file) {
        // Continue existing session — inject user query into chat
        const q = query || "Continue the investigation";
        appendEvent({
          seq: 0,
          type: "session.resumed",
          data: { session_id: currentSessionId, user_query: q },
        });
        startStream(currentSessionId, q);
      } else if (file) {
        // Upload pcap → creates session → start analysis
        const res = await uploadPcap(file);
        set({ currentSessionId: res.session_id, events: [] });
        get().bumpSidebarRefresh();
        const q =
          query ||
          "Analyze this pcap file and identify any security concerns.";
        // Inject user query into chat immediately for UX
        get().appendEvent({
          seq: 0,
          type: "session.created",
          data: {
            session_id: res.session_id,
            user_query: q,
            pcap_source: res.filename,
          },
        });
        get().startStream(res.session_id, q);
      }
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to start";
      get().appendEvent({
        seq: 0,
        type: "error",
        data: { phase: "client", message },
      });
    } finally {
      set({ loading: false });
    }
  },

  handleCancel: () => {
    const ctrl = get()._abortController;
    ctrl?.abort();
    set({ sessionActive: false, _abortController: null });
    get().appendEvent({
      seq: 0,
      type: "info",
      data: { message: "Investigation cancelled" },
    });
    get().bumpSidebarRefresh();
  },

  handleSelectSession: async (id) => {
    const ctrl = get()._abortController;
    ctrl?.abort();
    set({
      currentSessionId: id,
      events: [],
      sessionActive: false,
      _abortController: null,
    });

    try {
      const detail = await getSession(id);
      // Flatten rounds into a flat SSEEvent[] for the chat view.
      // For each round, inject a synthetic user-query event, then append
      // all stored events in sequence order.
      const flat: SSEEvent[] = [];
      for (const r of detail.rounds) {
        if (r.user_query) {
          // First round → session.created, subsequent → session.resumed
          const isFirst = r.round === 1;
          flat.push({
            seq: 0,
            type: isFirst ? "session.created" : "session.resumed",
            data: isFirst
              ? { session_id: id, user_query: r.user_query, pcap_source: "" }
              : { session_id: id, user_query: r.user_query, from_round: r.round - 1 },
          });
        }
        for (const ev of r.events) {
          flat.push({
            seq: ev.seq,
            type: ev.type as SSEEvent["type"],
            data: ev.data,
            timestamp: ev.timestamp,
          });
        }
      }
      set({ events: flat });
    } catch {
      // No stored events — empty state is fine
    }
  },

  handleNewSession: () => {
    const ctrl = get()._abortController;
    ctrl?.abort();
    set({
      currentSessionId: null,
      events: [],
      sessionActive: false,
      _abortController: null,
    });
  },

  handleAnalyzeFromManager: (sessionId) => {
    set({
      pcapManagerOpen: false,
      currentSessionId: sessionId,
      events: [],
    });
    get().bumpSidebarRefresh();
    const q = "Analyze this pcap file and identify any security concerns.";
    get().appendEvent({
      seq: 0,
      type: "session.created",
      data: { session_id: sessionId, user_query: q, pcap_source: "" },
    });
    get().startStream(sessionId, q);
  },
}));
