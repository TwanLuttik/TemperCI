/** Settings form helpers. Any API field key must be writable so new
 *  control-plane fields (e.g. mcp_token) are typable without a UI rebuild
 *  of a hardcoded key list. */

export function settingsFormValue(form: Record<string, string>, key: string): string {
  return form[key] ?? "";
}

export function settingsFormPatch(
  form: Record<string, string>,
  key: string,
  value: string,
): Record<string, string> {
  return { ...form, [key]: value };
}
