import fs from "fs";
import crypto from "crypto";
import bs58 from "bs58";
import dotenv from "dotenv";
import { Keypair } from "@solana/web3.js";

dotenv.config();

const ALGORITHM = 'aes-256-gcm';
const password = process.env.WALLET_PASSWORD;
const privateKeyBase58 = process.env.WALLET_PRIVATE_KEY;

if (!password) {
  console.error("Error: WALLET_PASSWORD not found in .env");
  process.exit(1);
}

if (!privateKeyBase58) {
  console.error("Error: WALLET_PRIVATE_KEY not found in .env");
  process.exit(1);
}

try {
  // Decode the base58 private key
  const secretKey = bs58.decode(privateKeyBase58);
  const keypair = Keypair.fromSecretKey(secretKey);
  console.log("Decoded WALLET_PRIVATE_KEY successfully.");
  console.log("Target Public Key:", keypair.publicKey.toString());

  // Format secret key as a JSON array string
  const jsonContent = JSON.stringify(Array.from(secretKey));

  // Backup old wallet.enc if it exists and hasn't been backed up yet
  if (fs.existsSync("wallet.enc")) {
    if (!fs.existsSync("wallet.enc.bak")) {
      fs.copyFileSync("wallet.enc", "wallet.enc.bak");
      console.log("Backed up old wallet.enc to wallet.enc.bak");
    } else {
      console.log("wallet.enc.bak already exists, skipping backup.");
    }
  }

  // Encrypt
  console.log("Encrypting secret key...");
  const key = crypto.createHash('sha256').update(String(password)).digest();
  const iv = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv(ALGORITHM, key, iv);
  
  let encrypted = cipher.update(Buffer.from(jsonContent, "utf-8"));
  encrypted = Buffer.concat([encrypted, cipher.final()]);
  const authTag = cipher.getAuthTag();
  const output = Buffer.concat([iv, authTag, encrypted]);

  fs.writeFileSync("wallet.enc", output);
  console.log("Successfully wrote new wallet.enc!");
  console.log("New wallet is now active!");

} catch (err) {
  console.error("Failed to encrypt and update wallet:", err.message);
  process.exit(1);
}
