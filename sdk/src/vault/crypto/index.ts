export {
  encrypt,
  decrypt,
  encryptString,
  decryptString,
  serialize,
  deserialize,
  encryptAndSerialize,
  deserializeAndDecrypt,
  toHex,
  fromHex,
  toBase64,
  fromBase64,
  generateKey,
  generateNonce,
  clearKey,
  isValidEncryptedData,
  KEY_SIZE,
  NONCE_SIZE,
  TAG_SIZE,
} from './aes';
export type { EncryptedData, SerializedEncryptedData } from './aes';

export { deriveKeyHKDF } from './hkdf';

export { split as shamirSplit, combine as shamirCombine } from './shamir';
export type { Share as ShamirShare } from './shamir';
