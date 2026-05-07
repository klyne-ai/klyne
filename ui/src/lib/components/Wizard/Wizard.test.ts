/**
 * Wizard.test.ts — covers all 4 wizard screens.
 *
 * Tests:
 *   Welcome_Renders         — welcome screen shows key text
 *   Welcome_NextAdvances    — clicking "Get started" triggers onnext
 *   Detection_RenderLoading — loading state shows spinner
 *   Detection_RenderData    — shows connector + provider status
 *   Detection_NextButton    — next button triggers onnext
 *   Detection_BackButton    — back button triggers onback
 *   ModelPick_Renders       — model pick screen renders pickers
 *   ModelPick_NextButton    — next button triggers onnext
 *   ModelPick_BackButton    — back triggers onback
 *   Done_RendersSaving      — saving=true shows spinner
 *   Done_RendersSuccess     — saving=false, no error shows success
 *   Done_DoneButton         — done button calls ondone
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import Welcome from './Welcome.svelte';
import Detection from './Detection.svelte';
import ModelPick from './ModelPick.svelte';
import Done from './Done.svelte';
import type { WizardDetectResponse } from '$lib/types.js';

const DETECT_DATA: WizardDetectResponse = {
  connectors: {
    claude_root: '/Users/test/.claude/projects',
    claude_ok: true,
    codex_root: '/Users/test/.codex/sessions',
    codex_ok: false
  },
  providers: {
    anthropic: true,
    openai: false,
    gemini: false,
    ollama: false
  },
  recommendations: [
    {
      task: 'summary',
      selected: { provider: 'anthropic', model: 'claude-haiku-4' },
      reason: 'Anthropic claude-haiku-4 picked'
    },
    {
      task: 'title',
      selected: { provider: 'anthropic', model: 'claude-haiku-4' },
      reason: 'Anthropic claude-haiku-4 picked'
    }
  ]
};

// ---------------------------------------------------------------------------
// Welcome screen
// ---------------------------------------------------------------------------

describe('Welcome', () => {
  it('Welcome_Renders — shows key heading text', () => {
    render(Welcome, { props: {} });
    expect(screen.getByTestId('wizard-welcome')).toBeInTheDocument();
    expect(screen.getByText('Welcome to agentdeck')).toBeInTheDocument();
  });

  it('Welcome_NextAdvances — clicking next button calls onnext', async () => {
    const onnext = vi.fn();
    render(Welcome, { props: { onnext } });

    const btn = screen.getByTestId('wizard-next-button');
    await fireEvent.click(btn);

    expect(onnext).toHaveBeenCalledOnce();
  });
});

// ---------------------------------------------------------------------------
// Detection screen
// ---------------------------------------------------------------------------

describe('Detection', () => {
  it('Detection_RenderLoading — loading=true shows loading indicator', () => {
    render(Detection, { props: { data: null, loading: true } });
    expect(screen.getByTestId('detection-loading')).toBeInTheDocument();
  });

  it('Detection_RenderError — error shows error panel', () => {
    render(Detection, { props: { data: null, error: 'Detection failed', loading: false } });
    expect(screen.getByTestId('detection-error')).toBeInTheDocument();
    expect(screen.getByTestId('detection-error')).toHaveTextContent('Detection failed');
  });

  it('Detection_RenderData — shows connector and provider status', () => {
    render(Detection, { props: { data: DETECT_DATA, loading: false } });

    expect(screen.getByTestId('wizard-detection')).toBeInTheDocument();
    expect(screen.getByTestId('detection-claude')).toBeInTheDocument();
    expect(screen.getByTestId('detection-codex')).toBeInTheDocument();
    expect(screen.getByTestId('detection-provider-anthropic')).toBeInTheDocument();
    expect(screen.getByTestId('detection-provider-openai')).toBeInTheDocument();
  });

  it('Detection_ClaudeOkIndicator — shows ✓ for available Claude', () => {
    render(Detection, { props: { data: DETECT_DATA, loading: false } });
    const claudeRow = screen.getByTestId('detection-claude');
    expect(claudeRow).toHaveTextContent('✓');
  });

  it('Detection_CodexNotOkIndicator — shows – for missing Codex', () => {
    render(Detection, { props: { data: DETECT_DATA, loading: false } });
    const codexRow = screen.getByTestId('detection-codex');
    expect(codexRow).toHaveTextContent('–');
  });

  it('Detection_NextButton — clicking next calls onnext', async () => {
    const onnext = vi.fn();
    render(Detection, { props: { data: DETECT_DATA, loading: false, onnext } });

    await fireEvent.click(screen.getByTestId('wizard-next-button'));
    expect(onnext).toHaveBeenCalledOnce();
  });

  it('Detection_BackButton — clicking back calls onback', async () => {
    const onback = vi.fn();
    render(Detection, { props: { data: DETECT_DATA, loading: false, onback } });

    await fireEvent.click(screen.getByTestId('wizard-back-button'));
    expect(onback).toHaveBeenCalledOnce();
  });

  it('Detection_NextDisabledDuringLoading — next button disabled when loading', () => {
    render(Detection, { props: { data: null, loading: true } });
    const btn = screen.getByTestId('wizard-next-button');
    expect((btn as HTMLButtonElement).disabled).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// ModelPick screen
// ---------------------------------------------------------------------------

describe('ModelPick', () => {
  it('ModelPick_Renders — model pick screen renders', () => {
    render(ModelPick, {
      props: {
        detectData: DETECT_DATA,
        summaryModel: 'auto',
        titleModel: 'auto'
      }
    });
    expect(screen.getByTestId('wizard-modelpick')).toBeInTheDocument();
  });

  it('ModelPick_ShowsPickers — both ModelPicker components are rendered', () => {
    render(ModelPick, {
      props: {
        detectData: DETECT_DATA,
        summaryModel: 'auto',
        titleModel: 'auto'
      }
    });

    // Both pickers render their select elements.
    const pickers = screen.getAllByTestId('model-picker-select');
    expect(pickers.length).toBeGreaterThanOrEqual(2);
  });

  it('ModelPick_NextButton — clicking next calls onnext', async () => {
    const onnext = vi.fn();
    render(ModelPick, {
      props: {
        detectData: DETECT_DATA,
        summaryModel: 'auto',
        titleModel: 'auto',
        onnext
      }
    });

    await fireEvent.click(screen.getByTestId('wizard-next-button'));
    expect(onnext).toHaveBeenCalledOnce();
  });

  it('ModelPick_BackButton — clicking back calls onback', async () => {
    const onback = vi.fn();
    render(ModelPick, {
      props: {
        detectData: DETECT_DATA,
        summaryModel: 'auto',
        titleModel: 'auto',
        onback
      }
    });

    await fireEvent.click(screen.getByTestId('wizard-back-button'));
    expect(onback).toHaveBeenCalledOnce();
  });

  it('ModelPick_SummaryChangeCallback — onsummarychange called on picker change', async () => {
    const onsummarychange = vi.fn();
    render(ModelPick, {
      props: {
        detectData: DETECT_DATA,
        summaryModel: 'auto',
        titleModel: 'auto',
        onsummarychange
      }
    });

    const selects = screen.getAllByTestId('model-picker-select');
    // First select is the summary picker.
    await fireEvent.change(selects[0], { target: { value: 'anthropic:claude-haiku-4' } });
    expect(onsummarychange).toHaveBeenCalledWith('anthropic:claude-haiku-4');
  });

  it('ModelPick_ClaudeOnlyHint — shows tip when only Anthropic is available', () => {
    render(ModelPick, {
      props: {
        detectData: DETECT_DATA, // anthropic=true, others=false
        summaryModel: 'auto',
        titleModel: 'auto'
      }
    });
    // Should show the "only have Anthropic" tip.
    expect(screen.getByText('Tip: Save on API costs')).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Done screen
// ---------------------------------------------------------------------------

describe('Done', () => {
  it('Done_RendersSaving — saving=true shows spinner', () => {
    render(Done, { props: { saving: true } });
    expect(screen.getByTestId('wizard-saving')).toBeInTheDocument();
  });

  it('Done_RendersSuccess — saving=false no error shows success message', () => {
    render(Done, { props: { saving: false, error: null } });
    expect(screen.getByTestId('wizard-done')).toBeInTheDocument();
    expect(screen.getByText("You're all set!")).toBeInTheDocument();
  });

  it('Done_ShowsError — error prop shows error panel', () => {
    render(Done, { props: { saving: false, error: 'Save failed' } });
    expect(screen.getByTestId('wizard-error')).toBeInTheDocument();
    expect(screen.getByTestId('wizard-error')).toHaveTextContent('Save failed');
  });

  it('Done_DoneButton — clicking calls ondone', async () => {
    const ondone = vi.fn();
    render(Done, { props: { saving: false, ondone } });

    await fireEvent.click(screen.getByTestId('wizard-done-button'));
    expect(ondone).toHaveBeenCalledOnce();
  });
});
