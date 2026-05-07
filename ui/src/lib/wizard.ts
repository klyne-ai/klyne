/**
 * wizard.ts — W15-specific API wrappers for the wizard flow.
 *
 * These supplement the W13-owned api.ts. The wizard complete endpoint
 * needs a request body that the W13 stub does not support.
 */

// WizardCompleteBody matches the Go WizardCompleteRequest struct.
interface WizardCompleteBody {
  summary_model: string;
  title_model: string;
  claude_enabled: boolean;
  codex_enabled: boolean;
}

const API_BASE =
  typeof import.meta !== 'undefined' &&
  typeof (import.meta as { env?: { VITE_API_BASE?: string } }).env !== 'undefined'
    ? ((import.meta as { env?: { VITE_API_BASE?: string } }).env?.VITE_API_BASE ?? '')
    : '';

/**
 * POST /wizard/complete — save wizard result with model selections.
 * Returns void (204 No Content).
 */
export async function postWizardComplete(
  summaryModel: string,
  titleModel: string,
  claudeEnabled = true,
  codexEnabled = true
): Promise<void> {
  const body: WizardCompleteBody = {
    summary_model: summaryModel,
    title_model: titleModel,
    claude_enabled: claudeEnabled,
    codex_enabled: codexEnabled
  };

  const res = await fetch(`${API_BASE}/wizard/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });

  if (!res.ok && res.status !== 204) {
    const text = await res.text().catch(() => '');
    throw new Error(`wizard complete failed (${res.status}): ${text}`);
  }
}
