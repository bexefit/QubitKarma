npm init -y
npm install @noble/post-quantum @scure/bip39 esbuild

cat << 'EOF' > entry.js
import { ml_kem512 } from '@noble/post-quantum/ml-kem.js';
import { ml_dsa44 } from '@noble/post-quantum/ml-dsa.js';
import * as bip39 from '@scure/bip39';
import { wordlist } from '@scure/bip39/wordlists/english.js';

// Expose variables globally on window
window.CryptoEngine = {
  ml_kem512,
  ml_dsa44,
  randomBytes: (len) => crypto.getRandomValues(new Uint8Array(len)),
  bip39,
  wordlist
};
EOF

npx esbuild entry.js --bundle --format=iife --outfile=dist/pqc-vault.js
