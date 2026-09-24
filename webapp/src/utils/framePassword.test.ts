import { describe, it, expect } from 'vitest';
import {
  FRAME_PASSWORD_MAX_BYTES,
  framePasswordBytes,
  newFramePasswordProblem,
} from './framePassword';

describe('newFramePasswordProblem', () => {
  it('accepts a matching password', () => {
    expect(newFramePasswordProblem('s3cret', 's3cret')).toBeNull();
  });

  it('refuses an empty password instead of turning protection off', () => {
    expect(newFramePasswordProblem('', '')).toMatch(/Turn off password/);
  });

  it('refuses a mismatch', () => {
    expect(newFramePasswordProblem('s3cret', 's3cre')).toMatch(/do not match/);
  });

  it('counts bytes, not characters', () => {
    const ascii = 'x'.repeat(FRAME_PASSWORD_MAX_BYTES);
    expect(newFramePasswordProblem(ascii, ascii)).toBeNull();
    const tooLong = ascii + 'x';
    expect(newFramePasswordProblem(tooLong, tooLong)).toMatch(/63 bytes/);
    // 32 two-byte characters: 32 characters, but 64 bytes.
    const accented = 'é'.repeat(32);
    expect(framePasswordBytes(accented)).toBe(64);
    expect(newFramePasswordProblem(accented, accented)).toMatch(/63 bytes/);
  });
});
