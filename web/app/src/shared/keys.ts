/**
 * Stable string key for persisted UI maps and React keys.
 *
 * Do not join ids with ":" here: ids are opaque and may contain
 * separators. JSON keeps the tuple boundary intact.
 */
export function keyOf(...parts: readonly string[]): string {
  return JSON.stringify(parts);
}
