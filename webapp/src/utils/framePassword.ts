// The firmware stores at most this many BYTES of password (not characters:
// a non-ASCII character takes several). The server enforces it too.
export const FRAME_PASSWORD_MAX_BYTES = 63;

export function framePasswordBytes(password: string): number {
  return new TextEncoder().encode(password).length;
}

// newFramePasswordProblem returns why a new password for the frame itself,
// typed twice, cannot be sent yet, or null when it can. An empty password is
// refused here on purpose: turning the frame's password off is its own
// action, with its own confirmation, not something a blank field does.
export function newFramePasswordProblem(
  password: string,
  repeat: string
): string | null {
  if (password === '') {
    return 'Enter the new password. To remove the password, use "Turn off password on the frame" instead.';
  }
  if (framePasswordBytes(password) > FRAME_PASSWORD_MAX_BYTES) {
    return `The frame accepts at most ${FRAME_PASSWORD_MAX_BYTES} bytes of password.`;
  }
  if (password !== repeat) {
    return 'The two passwords do not match.';
  }
  return null;
}
