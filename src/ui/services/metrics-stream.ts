import { currentSynoToken, synoTokenSubprotocol } from "./terminal-socket.js";

export type Reading = { name: string; value: number };
export type Metrics = { streamId: string; sequence: number; timestamp: number; cpu: Reading[]; ram: number | null; disks: Reading[]; fans: Reading[]; temperatures: Reading[] };

export function isMetrics(value: unknown): value is Metrics {
  if (!value || typeof value !== "object") return false;
  const m = value as Metrics;
  const readings = (values: unknown): boolean => Array.isArray(values) && values.length <= 4096 && values.every(v => v && typeof v.name === "string" && Number.isFinite(v.value));
  return typeof m.streamId === "string" && m.streamId.length > 0 && Number.isSafeInteger(m.sequence) && m.sequence > 0
    && Number.isFinite(m.timestamp) && m.timestamp > 0 && (m.ram === null || Number.isFinite(m.ram))
    && [m.cpu, m.disks, m.fans, m.temperatures].every(readings);
}

export class MetricsStream {
  private socket: WebSocket | null = null;
  private timer: number | null = null;
  private watchdog: number | null = null;
  private active = false;
  private retry = 1000;
  constructor(private receive: (samples: Metrics[], fresh: boolean) => void, private disconnected: () => void) {}

  setActive(active: boolean): void {
    if (this.active === active) return;
    this.active = active;
    if (active) { this.retry = 1000; this.connect(); }
    else this.stop();
  }
  dispose(): void { this.active = false; this.stop(); }
  private stop(): void {
    if (this.timer !== null) window.clearTimeout(this.timer);
    if (this.watchdog !== null) window.clearTimeout(this.watchdog);
    this.timer = this.watchdog = null;
    const socket = this.socket;
    this.socket = null;
    socket?.close(1000, "Monitor inactive");
    this.disconnected();
  }
  private connect(): void {
    if (!this.active) return;
    try {
      const token = currentSynoToken();
      const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
      const socket = new WebSocket(`${scheme}//${window.location.host}/diskshell/metrics`, token ? [synoTokenSubprotocol(token)] : undefined);
      this.socket = socket;
      const armWatchdog = () => {
        if (this.watchdog !== null) window.clearTimeout(this.watchdog);
        this.watchdog = window.setTimeout(() => this.recover(socket, 4000, "Metrics timeout"), 20000);
      };
      armWatchdog();
      socket.addEventListener("message", event => {
        if (this.socket !== socket) return;
        try {
          const message = JSON.parse(event.data);
          if (message.type !== "metrics" || !Number.isFinite(message.sentAt) || !Array.isArray(message.samples) || !message.samples.length || message.samples.length > 31 || !message.samples.every(isMetrics)) throw new Error("Invalid metrics");
          this.receive(message.samples, message.sentAt - message.samples.at(-1).timestamp < 5000);
          this.retry = 1000;
          armWatchdog();
        } catch { this.recover(socket, 4002, "Invalid metrics response"); }
      });
      socket.addEventListener("close", event => {
        if (this.socket !== socket) return;
        this.socket = null;
        if (this.watchdog !== null) window.clearTimeout(this.watchdog);
        this.watchdog = null;
        this.disconnected();
        // A revoked session stays disconnected until the view is reactivated.
        if (event.code !== 1008) this.scheduleReconnect();
      });
      socket.addEventListener("error", () => { if (this.socket === socket) socket.close(); });
    } catch { this.disconnected(); this.scheduleReconnect(); }
  }
  private recover(socket: WebSocket, code: number, reason: string): void {
    if (this.socket !== socket) return;
    // A stalled close handshake must not hold the UI or the next connection hostage.
    // Late events from this socket are ignored by the identity guards above.
    this.socket = null;
    if (this.watchdog !== null) window.clearTimeout(this.watchdog);
    this.watchdog = null;
    this.disconnected();
    try { socket.close(code, reason); }
    finally { this.scheduleReconnect(); }
  }
  private scheduleReconnect(): void {
    if (!this.active || this.timer !== null) return;
    this.timer = window.setTimeout(() => { this.timer = null; this.connect(); }, this.retry);
    this.retry = Math.min(30000, this.retry * 2);
  }
}
