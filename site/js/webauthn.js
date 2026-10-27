// Passkeys and security keys in the browser. The server sends WebAuthn
// options as JSON with binary values in base64url; navigator.credentials
// wants them as ArrayBuffers, and its answer goes back the other way. No DOM
// access, so Node can test the conversions.

const toBytes = (s) => {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/');
  return Uint8Array.from(atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4)), (c) => c.charCodeAt(0)).buffer;
};
const toB64url = (buf) => {
  let bin = '';
  for (const b of new Uint8Array(buf)) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
};

// The options of navigator.credentials.create() from the server's JSON.
export function creationOptions(o) {
  return {
    ...o,
    challenge: toBytes(o.challenge),
    user: { ...o.user, id: toBytes(o.user.id) },
    excludeCredentials: (o.excludeCredentials || []).map((c) => ({ ...c, id: toBytes(c.id) })),
  };
}

// The options of navigator.credentials.get().
export function requestOptions(o) {
  return {
    ...o,
    challenge: toBytes(o.challenge),
    allowCredentials: (o.allowCredentials || []).map((c) => ({ ...c, id: toBytes(c.id) })),
  };
}

// A credential as the server reads it: a new one (attestation) or a
// signature (assertion).
export function credentialJSON(cred) {
  const r = cred.response;
  const response = { clientDataJSON: toB64url(r.clientDataJSON) };
  if (r.attestationObject) {
    response.attestationObject = toB64url(r.attestationObject);
    if (r.getTransports) response.transports = r.getTransports();
  } else {
    response.authenticatorData = toB64url(r.authenticatorData);
    response.signature = toB64url(r.signature);
    if (r.userHandle) response.userHandle = toB64url(r.userHandle);
  }
  return {
    id: cred.id,
    rawId: toB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment || undefined,
    clientExtensionResults: cred.getClientExtensionResults?.() || {},
    response,
  };
}

export const supported = () => typeof globalThis.PublicKeyCredential === 'function';

// The browser offers saved passkeys in the name field (conditional UI).
export async function autofillSupported() {
  try {
    return supported() && !!(await PublicKeyCredential.isConditionalMediationAvailable?.());
  } catch {
    return false;
  }
}

export { toBytes, toB64url };
