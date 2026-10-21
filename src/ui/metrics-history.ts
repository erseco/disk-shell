import type { Metrics } from "./services/metrics-stream.js";
export type Point = { timestamp: number; value: number | null };
export const historyWindow = 60000;

export function mergeHistory(history: Metrics[], incoming: Metrics[]): Metrics[] {
  if (!incoming.length) return history;
  const streamId = incoming.at(-1)!.streamId;
  const bySequence = new Map(history.filter(sample => sample.streamId === streamId).map(sample => [sample.sequence, sample]));
  incoming.filter(sample => sample.streamId === streamId).forEach(sample => bySequence.set(sample.sequence, sample));
  const sorted = [...bySequence.values()].sort((a, b) => a.sequence - b.sequence);
  const end = sorted.at(-1)?.timestamp || 0;
  return sorted.filter(sample => sample.timestamp >= end - historyWindow).slice(-31);
}
export function chartRange(values: number[], unit: string): [number, number] {
  if (!values.length) return [0, 1];
  const low = Math.min(...values), high = Math.max(...values);
  const padding = high > low ? (high - low) * .15 : unit === " RPM" ? 1 : .01;
  return [Math.max(0, low - padding), high + padding];
}
export function chartPath(points: Point[], range: [number, number], end: number): string {
  const [low, high] = range;
  let previous: number | null = null;
  return points.map(point => {
    if (point.value === null) { previous = null; return ""; }
    const command = previous === null || point.timestamp - previous > 5000 ? "M" : "L";
    previous = point.timestamp;
    return `${command}${60 - (end - point.timestamp) / 1000},${21 - (point.value - low) / (high - low) * 20}`;
  }).join(" ");
}
