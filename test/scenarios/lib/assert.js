export class AssertionFailed extends Error {
  constructor(message, { actual, expected, context } = {}) {
    super(message);
    this.name = "AssertionFailed";
    this.actual = actual;
    this.expected = expected;
    this.context = context;
  }
}

export function eq(actual, expected, message = "values differ") {
  if (actual !== expected) {
    throw new AssertionFailed(message, { actual, expected });
  }
}

export function truthy(v, message = "expected truthy value") {
  if (!v) throw new AssertionFailed(message, { actual: v, expected: "truthy" });
}

export function gte(actual, n, message) {
  if (!(actual >= n)) {
    throw new AssertionFailed(message ?? `expected >= ${n}, got ${actual}`, {
      actual,
      expected: `>= ${n}`,
    });
  }
}

export function lt(actual, n, message) {
  if (!(actual < n)) {
    throw new AssertionFailed(message ?? `expected < ${n}, got ${actual}`, {
      actual,
      expected: `< ${n}`,
    });
  }
}

export function includes(haystack, needle, message) {
  const ok = typeof haystack === "string"
    ? haystack.toLowerCase().includes(String(needle).toLowerCase())
    : Array.isArray(haystack)
      ? haystack.includes(needle)
      : false;
  if (!ok) {
    throw new AssertionFailed(message ?? `expected to contain "${needle}"`, {
      actual: haystack,
      expected: `includes "${needle}"`,
    });
  }
}

export function match(text, regex, message) {
  if (!regex.test(text)) {
    throw new AssertionFailed(message ?? `expected to match ${regex}`, {
      actual: text,
      expected: regex.toString(),
    });
  }
}
