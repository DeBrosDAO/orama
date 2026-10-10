/** The docs sections the sidebar switches between. Each is a folder under src/docs/. */
export type Persona =
  | "start"
  | "developer"
  | "operator"
  | "architecture"
  | "blockchain"
  | "privacy"
  | "rootwallet"
  | "contributor";

export const PERSONA_ORDER: Persona[] = [
  "start",
  "developer",
  "operator",
  "architecture",
  "blockchain",
  "privacy",
  "rootwallet",
  "contributor",
];

export function isPersona(value: string): value is Persona {
  return (PERSONA_ORDER as string[]).includes(value);
}
