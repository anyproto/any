export function shouldHydrateMarkdownEditor({
  hydratedIdentity,
  identityKey,
  markdown,
}: {
  hydratedIdentity: string | null;
  identityKey: string;
  markdown: string | null;
}) {
  return markdown != null && hydratedIdentity !== identityKey;
}
