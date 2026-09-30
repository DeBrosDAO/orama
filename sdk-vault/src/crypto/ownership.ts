import { ed25519 } from '@noble/curves/ed25519.js';
import { sha256 } from '@noble/hashes/sha2.js';
import { bytesToHex } from '@noble/hashes/utils.js';

const SEED_SIZE = 32;

export function publicKeyFromSeed(seed: Uint8Array): Uint8Array {
  if (seed.length !== SEED_SIZE) {
    throw new Error(`Ed25519 seed must be ${SEED_SIZE} bytes`);
  }
  return ed25519.getPublicKey(seed);
}

export function identityFromSeed(seed: Uint8Array): string {
  return bytesToHex(sha256(publicKeyFromSeed(seed)));
}

export function signUtf8(seed: Uint8Array, message: string): string {
  const encoded = new TextEncoder().encode(message);
  return bytesToHex(ed25519.sign(encoded, seed));
}

export function putMessage(identityHex: string, name: string, version: number): string {
  return `vault-secret-put-v1:${identityHex}:${name}:${version}`;
}

export function getMessage(identityHex: string, name: string, timestamp: number): string {
  return `vault-secret-get-v1:${identityHex}:${name}:${timestamp}`;
}

export function deleteMessage(identityHex: string, name: string, timestamp: number): string {
  return `vault-secret-delete-v1:${identityHex}:${name}:${timestamp}`;
}

export function listMessage(identityHex: string, timestamp: number): string {
  return `vault-secret-list-v1:${identityHex}:${timestamp}`;
}

export function unixSeconds(): number {
  return Math.floor(Date.now() / 1000);
}
