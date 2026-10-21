import { MetricsStream } from "../services/metrics-stream.js";
import type { Metrics, Reading } from "../services/metrics-stream.js";
import { chartPath, chartRange, mergeHistory } from "../metrics-history.js";
import type { Point } from "../metrics-history.js";
import { messages, currentDSMLanguage } from "../i18n.js";

type Series = { key: string; label: string; value: string; detail: string; points: Point[]; unit: string; color?: string };
type Monitor = {
  text: typeof messages; history: Metrics[]; stale: boolean; selected: string; left: number; top: number; panelHeight: number;
  stream: MetricsStream | null; observer: IntersectionObserver | null; resizeObserver: ResizeObserver | null; visible: boolean;
  visibilityHandler: (() => void) | null; outsideHandler: ((event: MouseEvent) => void) | null;
  $el: HTMLElement; $refs: { details?: HTMLElement }; $nextTick(fn: () => void): void;
  tiles: Series[]; cores: Series[]; activeSeries: Series[]; end: number; cpuValue: string;
  positionDetails(): void; syncVisibility(): void; closeDetails(): void; path(series: Series): string; scaleLabel(series: Series): string;
};
const maximum = (values: Reading[]): number | null => values.length ? Math.max(...values.map(v => v.value)) : null;
const formatValue = (value: number | null, unit: string): string => value === null ? "—" : Math.round(value) + unit;
const detail = (readings: Reading[], unit: string): string => readings.map(v => `${v.name}: ${formatValue(v.value, unit)}`).join("\n");

export const systemMonitorComponent = {
  template: `<div class="diskshell-monitor" :class="{ stale }" role="group" :aria-label="text.systemMonitor">
    <button type="button" class="diskshell-metric cpu" :aria-label="text.cpuCores + ': ' + (stale ? text.metricsUnavailable : cpuValue)" aria-haspopup="dialog" :aria-expanded="selected === 'cpu'" @click="openDetails('cpu', $event)">
      <div class="diskshell-metric-caption"><span>CPU</span><b>{{ stale ? '—' : cpuValue }}</b></div>
      <svg viewBox="0 0 60 22" preserveAspectRatio="none" aria-hidden="true"><path class="baseline" d="M0 21H60"/><path v-for="core in cores" :key="core.key" class="curve" :d="path(core)" :style="{ stroke: core.color }"/></svg>
    </button>
    <button v-for="tile in tiles" :key="tile.key" type="button" class="diskshell-metric" :class="tile.key" :aria-label="tile.label + ': ' + (stale ? text.metricsUnavailable : tile.value)" aria-haspopup="dialog" :aria-expanded="selected === tile.key" @click="openDetails(tile.key, $event)">
      <div class="diskshell-metric-caption"><span>{{ tile.label }}</span><b>{{ stale ? '—' : tile.value }}</b></div>
      <svg viewBox="0 0 60 22" preserveAspectRatio="none" aria-hidden="true"><path class="baseline" d="M0 21H60"/><path class="curve" :d="path(tile)"/></svg>
    </button>
    <div v-if="selected" ref="details" class="diskshell-metric-detail" role="dialog" tabindex="-1" :aria-label="text.systemMonitor" :style="{ left: left + 'px', top: top + 'px', maxHeight: panelHeight + 'px' }" @keydown.esc.stop.prevent="closeDetails">
      <header><strong>{{ text.systemMonitor }}</strong><button type="button" :aria-label="text.closeMetrics" @click="closeDetails">×</button></header>
      <p v-if="stale" role="status">{{ text.metricsStale }}</p>
      <p>{{ text.chartHistory }}</p>
      <div class="diskshell-metric-readings" tabindex="0" :aria-label="text.metricReadings">
        <section v-for="series in activeSeries" :key="series.key" :style="{ color: series.color }"><strong>{{ series.label }}: {{ stale ? '—' : series.value }}</strong><div>{{ series.detail }}</div><svg viewBox="0 0 60 22" preserveAspectRatio="none" aria-hidden="true"><path class="curve" :d="path(series)"/></svg><div>{{ scaleLabel(series) }}</div></section>
        <p v-if="!activeSeries.length">{{ text.metricsUnavailable }}</p>
      </div>
    </div>
  </div>`,
  data() { return { text: messages, history: [], stale: true, selected: "", left: 0, top: 0, panelHeight: 300,
    stream: null, observer: null, resizeObserver: null, visible: false, visibilityHandler: null, outsideHandler: null }; },
  computed: {
    end(this: Monitor): number { return this.history.at(-1)?.timestamp || 0; },
    cpuValue(this: Monitor): string {
      const cpu = this.history.at(-1)?.cpu || [];
      return formatValue(cpu.length ? cpu.reduce((sum, core) => sum + core.value, 0) / cpu.length : null, "%");
    },
    cores(this: Monitor): Series[] {
      const names = [...new Set(this.history.flatMap(sample => sample.cpu.map(core => core.name)))].sort((a, b) => Number(a.slice(3)) - Number(b.slice(3)));
      return names.map((name, index) => ({ key: name, label: name, unit: "%", color: `hsl(${190 + index * 137.5}, 75%, 72%)`, detail: "",
        value: formatValue(this.history.at(-1)?.cpu.find(core => core.name === name)?.value ?? null, "%"),
        points: this.history.map(sample => ({ timestamp: sample.timestamp, value: sample.cpu.find(core => core.name === name)?.value ?? null })),
      }));
    },
    tiles(this: Monitor): Series[] {
      const definitions = [
        { key: "ram", label: "RAM", unit: "%", value: (s: Metrics) => s.ram, detail: (_s: Metrics) => messages.ramUsage },
        { key: "disk", label: messages.diskUsage, unit: "%", value: (s: Metrics) => maximum(s.disks), detail: (s: Metrics) => detail(s.disks, "%") },
        { key: "fan", label: messages.fanSpeed, unit: " RPM", value: (s: Metrics) => maximum(s.fans), detail: (s: Metrics) => detail(s.fans, " RPM") },
        { key: "temperature", label: messages.diskTemperature, unit: "°C", value: (s: Metrics) => maximum(s.temperatures), detail: (s: Metrics) => detail(s.temperatures, " °C") },
      ];
      const latest = this.history.at(-1);
      return definitions.map(d => ({ key: d.key, label: d.label, unit: d.unit,
        value: formatValue(latest ? d.value(latest) : null, d.key === "temperature" ? "°" : d.unit),
        detail: latest && d.value(latest) !== null ? d.detail(latest) : messages.metricsUnavailable,
        points: this.history.map(sample => ({ timestamp: sample.timestamp, value: d.value(sample) })),
      }));
    },
    activeSeries(this: Monitor): Series[] { return this.selected === "cpu" ? this.cores : this.tiles.filter(tile => tile.key === this.selected); },
  },
  mounted(this: Monitor) {
    this.stream = new MetricsStream((samples, fresh) => { this.history = mergeHistory(this.history, samples); this.stale = !fresh; }, () => { this.stale = true; });
    this.visibilityHandler = () => this.syncVisibility();
    document.addEventListener("visibilitychange", this.visibilityHandler);
    this.observer = new IntersectionObserver(entries => { this.visible = entries.some(entry => entry.isIntersecting); this.syncVisibility(); });
    this.observer.observe(this.$el);
    this.resizeObserver = new ResizeObserver(() => this.positionDetails());
    this.resizeObserver.observe(this.$el.closest(".diskshell-shell")!);
    this.outsideHandler = event => { if (!this.$el.contains(event.target as Node)) this.selected = ""; };
    document.addEventListener("click", this.outsideHandler);
  },
  beforeDestroy(this: Monitor) {
    this.stream?.dispose(); this.stream = null; this.observer?.disconnect(); this.resizeObserver?.disconnect();
    if (this.visibilityHandler) document.removeEventListener("visibilitychange", this.visibilityHandler);
    if (this.outsideHandler) document.removeEventListener("click", this.outsideHandler);
  },
  methods: {
    syncVisibility(this: Monitor) { this.stream?.setActive(this.visible && !document.hidden); if (!this.visible || document.hidden) this.selected = ""; },
    path(this: Monitor, series: Series): string { return chartPath(series.points, chartRange(series.points.flatMap(p => p.value === null ? [] : [p.value]), series.unit), this.end); },
    scaleLabel(this: Monitor, series: Series): string {
      const values = series.points.flatMap(p => p.value === null ? [] : [p.value]);
      if (!values.length) return messages.metricsUnavailable;
      const [low, high] = chartRange(values, series.unit);
      const format = new Intl.NumberFormat(currentDSMLanguage() === "ger" ? "de-DE" : "en-US", { maximumFractionDigits: 2 });
      return `${messages.chartScale}: ${format.format(low)}–${format.format(high)}${series.unit}`;
    },
    openDetails(this: Monitor, key: string, event: MouseEvent) {
      if (this.selected === key) { this.closeDetails(); return; }
      this.selected = key;
      this.positionDetails();
      this.$nextTick(() => this.$refs.details?.focus());
    },
    positionDetails(this: Monitor) {
      if (!this.selected) return;
      const button = this.$el.querySelector<HTMLElement>(`.diskshell-metric.${this.selected}`);
      if (!button || !button.getClientRects().length) { this.selected = ""; return; }
      const shell = this.$el.closest(".diskshell-shell") as HTMLElement;
      const anchor = button.getBoundingClientRect(), bounds = shell.getBoundingClientRect();
      this.left = Math.max(8, Math.min(anchor.left - bounds.left, bounds.width - 296));
      this.top = anchor.bottom - bounds.top + 6;
      this.panelHeight = Math.min(360, bounds.height - this.top - 10);
      if (this.panelHeight < 80) this.selected = "";
    },
    closeDetails(this: Monitor) {
      const key = this.selected; this.selected = "";
      this.$el.querySelector<HTMLButtonElement>(`.diskshell-metric.${key}`)?.focus();
    },
  },
};
