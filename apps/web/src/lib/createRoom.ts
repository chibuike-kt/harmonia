import { apiFetch } from "./api";

interface CreatedRoom {
  id: string;
}

/**
 * Creates a room with no name — the backend defaults it to the literal
 * placeholder "New room" and generates a real title asynchronously from
 * the room's first message once it exists (ADR-004's addendum on
 * nameless room creation). Every "New room" action in the app goes
 * through this one function so a human lands straight in an empty room
 * and starts typing, never a naming form first.
 */
export function createRoom(): Promise<CreatedRoom> {
  return apiFetch<CreatedRoom>("/v1/rooms", { method: "POST", body: {} });
}
