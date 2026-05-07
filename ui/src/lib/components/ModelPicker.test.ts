/**
 * ModelPicker.test.ts
 *
 * Tests:
 *   ModelPicker_RendersDropdown       — renders select element
 *   ModelPicker_ShowsRecommendation   — shows recommended badge when provided
 *   ModelPicker_NoRecommendation      — no badge when recommended=null
 *   ModelPicker_ChangeEmitsValue      — onchange called with selected value
 *   ModelPicker_AutoOption            — "auto" option is always present
 *   ModelPicker_RecommendedInOptions  — recommended model appears in dropdown
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import ModelPicker from './ModelPicker.svelte';
import type { DetectedProviders, WizardRecommendation } from '$lib/types.js';

const noDetected: DetectedProviders = {
  anthropic: false,
  openai: false,
  gemini: false,
  ollama: false
};

const withAnthropic: DetectedProviders = {
  anthropic: true,
  openai: false,
  gemini: false,
  ollama: false
};

const mockRec: WizardRecommendation = {
  task: 'summary',
  selected: { provider: 'anthropic', model: 'claude-haiku-4' },
  reason: 'Anthropic claude-haiku-4 picked: lightest Claude model'
};

describe('ModelPicker', () => {
  it('ModelPicker_RendersDropdown — select element is present', () => {
    render(ModelPicker, {
      props: {
        task: 'summarize',
        detected: noDetected,
        recommended: null,
        value: 'auto'
      }
    });

    expect(screen.getByTestId('model-picker-select')).toBeInTheDocument();
  });

  it('ModelPicker_ShowsRecommendation — badge visible when recommended provided', () => {
    render(ModelPicker, {
      props: {
        task: 'summarize',
        detected: withAnthropic,
        recommended: mockRec,
        value: 'auto'
      }
    });

    expect(screen.getByTestId('model-picker-recommendation')).toBeInTheDocument();
    expect(screen.getByText('Recommended')).toBeInTheDocument();
    expect(screen.getByText('anthropic / claude-haiku-4')).toBeInTheDocument();
    expect(screen.getByText(mockRec.reason)).toBeInTheDocument();
  });

  it('ModelPicker_NoRecommendation — no badge when recommended is null', () => {
    render(ModelPicker, {
      props: {
        task: 'title',
        detected: noDetected,
        recommended: null,
        value: 'auto'
      }
    });

    expect(screen.queryByTestId('model-picker-recommendation')).not.toBeInTheDocument();
  });

  it('ModelPicker_ChangeEmitsValue — onchange called with new value', async () => {
    const onchange = vi.fn();

    render(ModelPicker, {
      props: {
        task: 'summarize',
        detected: withAnthropic,
        recommended: mockRec,
        value: 'auto',
        onchange
      }
    });

    const select = screen.getByTestId('model-picker-select') as HTMLSelectElement;
    // Simulate selection of the recommended value.
    await fireEvent.change(select, { target: { value: 'anthropic:claude-haiku-4' } });

    expect(onchange).toHaveBeenCalledWith('anthropic:claude-haiku-4');
  });

  it('ModelPicker_AutoOption — "auto" option always present', () => {
    render(ModelPicker, {
      props: {
        task: 'title',
        detected: noDetected,
        recommended: null,
        value: 'auto'
      }
    });

    const select = screen.getByTestId('model-picker-select') as HTMLSelectElement;
    const options = Array.from(select.options).map((o) => o.value);
    expect(options).toContain('auto');
  });

  it('ModelPicker_RecommendedInOptions — recommended model option is in dropdown', () => {
    render(ModelPicker, {
      props: {
        task: 'summarize',
        detected: noDetected,
        recommended: mockRec,
        value: 'auto'
      }
    });

    const select = screen.getByTestId('model-picker-select') as HTMLSelectElement;
    const options = Array.from(select.options).map((o) => o.value);
    expect(options).toContain('anthropic:claude-haiku-4');
  });

  it('ModelPicker_TaskLabelSummarize — shows "Summarize model" label', () => {
    render(ModelPicker, {
      props: {
        task: 'summarize',
        detected: noDetected,
        recommended: null,
        value: 'auto'
      }
    });

    expect(screen.getByText('Summarize model')).toBeInTheDocument();
  });

  it('ModelPicker_TaskLabelTitle — shows "Title model" label', () => {
    render(ModelPicker, {
      props: {
        task: 'title',
        detected: noDetected,
        recommended: null,
        value: 'auto'
      }
    });

    expect(screen.getByText('Title model')).toBeInTheDocument();
  });
});
