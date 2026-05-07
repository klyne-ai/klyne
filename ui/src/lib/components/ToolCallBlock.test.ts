/**
 * ToolCallBlock.test.ts
 *
 * Tests:
 *   TestToolCallBlock_HappyPath     — renders tool name, expand/collapse
 *   TestToolCallBlock_WithResult    — shows success status and output
 *   TestToolCallBlock_ErrorResult   — shows error status and error output
 *   TestToolCallBlock_EmptyState    — pending state when no result
 *   TestToolCallBlock_JsonPretty    — pretty-prints valid JSON input
 *   TestToolCallBlock_InvalidJson   — falls back to raw string for invalid JSON
 *   TestToolCallBlock_Toggle        — expand collapses and expands input
 */

import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import ToolCallBlock from './ToolCallBlock.svelte';
import type { ToolCall, ToolResult } from '$lib/types.js';

function makeToolCall(overrides: Partial<ToolCall> = {}): ToolCall {
  return {
    id: 'tc-1',
    name: 'bash',
    input: JSON.stringify({ command: 'ls -la' }),
    ...overrides
  };
}

function makeToolResult(overrides: Partial<ToolResult> = {}): ToolResult {
  return {
    id: 'tc-1',
    output: 'file1.txt\nfile2.txt',
    is_error: false,
    ...overrides
  };
}

describe('TestToolCallBlock_HappyPath', () => {
  it('renders tool name', () => {
    render(ToolCallBlock, { props: { toolCall: makeToolCall({ name: 'bash' }) } });
    expect(screen.getByTestId('tool-name')).toHaveTextContent('bash');
  });

  it('is collapsed by default (input not visible)', () => {
    render(ToolCallBlock, { props: { toolCall: makeToolCall() } });
    expect(screen.queryByTestId('tool-input')).not.toBeInTheDocument();
  });

  it('renders the block container', () => {
    render(ToolCallBlock, { props: { toolCall: makeToolCall() } });
    expect(screen.getByTestId('tool-call-block')).toBeInTheDocument();
  });
});

describe('TestToolCallBlock_WithResult', () => {
  it('shows ✓ ok when result is not an error', () => {
    render(ToolCallBlock, {
      props: { toolCall: makeToolCall(), toolResult: makeToolResult({ is_error: false }) }
    });
    expect(screen.getByText('✓ ok')).toBeInTheDocument();
  });

  it('shows output after expanding', async () => {
    render(ToolCallBlock, {
      props: { toolCall: makeToolCall(), toolResult: makeToolResult({ output: 'my output' }) }
    });
    const btn = screen.getByRole('button');
    await fireEvent.click(btn);
    expect(screen.getByTestId('tool-output')).toHaveTextContent('my output');
  });
});

describe('TestToolCallBlock_ErrorResult', () => {
  it('shows ✗ error when result is an error', () => {
    render(ToolCallBlock, {
      props: {
        toolCall: makeToolCall(),
        toolResult: makeToolResult({ is_error: true, output: 'command not found' })
      }
    });
    expect(screen.getByText('✗ error')).toBeInTheDocument();
  });

  it('shows error output in error styling after expanding', async () => {
    render(ToolCallBlock, {
      props: {
        toolCall: makeToolCall(),
        toolResult: makeToolResult({ is_error: true, output: 'error message' })
      }
    });
    const btn = screen.getByRole('button');
    await fireEvent.click(btn);
    const output = screen.getByTestId('tool-output');
    expect(output).toHaveTextContent('error message');
    expect(output.classList.contains('text-red-300')).toBe(true);
  });
});

describe('TestToolCallBlock_EmptyState', () => {
  it('shows "pending" when no tool result provided', () => {
    render(ToolCallBlock, { props: { toolCall: makeToolCall() } });
    expect(screen.getByText('pending')).toBeInTheDocument();
  });
});

describe('TestToolCallBlock_JsonPretty', () => {
  it('pretty-prints valid JSON input', async () => {
    const tc = makeToolCall({ input: '{"key":"value","num":42}' });
    render(ToolCallBlock, { props: { toolCall: tc } });
    const btn = screen.getByRole('button');
    await fireEvent.click(btn);
    const inputEl = screen.getByTestId('tool-input');
    // Pretty-printed JSON should include newlines
    expect(inputEl.textContent).toContain('"key"');
    expect(inputEl.textContent).toContain('"value"');
  });
});

describe('TestToolCallBlock_InvalidJson', () => {
  it('falls back to raw string for invalid JSON input', async () => {
    const tc = makeToolCall({ input: 'not-json-at-all' });
    render(ToolCallBlock, { props: { toolCall: tc } });
    const btn = screen.getByRole('button');
    await fireEvent.click(btn);
    expect(screen.getByTestId('tool-input')).toHaveTextContent('not-json-at-all');
  });
});

describe('TestToolCallBlock_Toggle', () => {
  it('expands to show input on click', async () => {
    render(ToolCallBlock, { props: { toolCall: makeToolCall() } });
    const btn = screen.getByRole('button');
    await fireEvent.click(btn);
    expect(screen.getByTestId('tool-input')).toBeInTheDocument();
  });

  it('collapses back on second click', async () => {
    render(ToolCallBlock, { props: { toolCall: makeToolCall() } });
    const btn = screen.getByRole('button');
    await fireEvent.click(btn);
    expect(screen.getByTestId('tool-input')).toBeInTheDocument();
    await fireEvent.click(btn);
    expect(screen.queryByTestId('tool-input')).not.toBeInTheDocument();
  });

  it('sets aria-expanded correctly', async () => {
    render(ToolCallBlock, { props: { toolCall: makeToolCall() } });
    const btn = screen.getByRole('button');
    expect(btn).toHaveAttribute('aria-expanded', 'false');
    await fireEvent.click(btn);
    expect(btn).toHaveAttribute('aria-expanded', 'true');
  });
});
