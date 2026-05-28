import fs from 'fs';
import crypto from 'crypto';
import dotenv from 'dotenv';

dotenv.config();

const ALGORITHM = 'aes-256-gcm';
// Mengambil password dari .env
const password = process.env.WALLET_PASSWORD;

if (!password) {
    console.error('Error: WALLET_PASSWORD tidak ditemukan di file .env');
    console.error('Silakan tambahkan WALLET_PASSWORD=password_rahasia_anda di .env');
    process.exit(1);
}

// Membuat 32-byte key dari password menggunakan SHA-256
const key = crypto.createHash('sha256').update(String(password)).digest();

const walletPath = './wallet.json';
const encPath = './wallet.enc';

if (!fs.existsSync(walletPath)) {
    console.error('Error: File ' + walletPath + ' tidak ditemukan.');
    process.exit(1);
}

const data = fs.readFileSync(walletPath);

// Generate Initialization Vector (IV) yang unik
const iv = crypto.randomBytes(12);

// Membuat cipher menggunakan algoritma AES-256-GCM
const cipher = crypto.createCipheriv(ALGORITHM, key, iv);

let encrypted = cipher.update(data);
encrypted = Buffer.concat([encrypted, cipher.final()]);

// Mendapatkan Authentication Tag untuk memastikan integritas data
const authTag = cipher.getAuthTag();

// Menggabungkan IV (12 bytes) + Auth Tag (16 bytes) + Encrypted Data
const output = Buffer.concat([iv, authTag, encrypted]);

fs.writeFileSync(encPath, output);

console.log('Sukses! File wallet.json berhasil dienkripsi dan disimpan sebagai wallet.enc');
console.log('PENTING: Pastikan untuk menyesuaikan kode di sistem Anda agar mendekripsi file wallet.enc ini ke dalam memori secara on-the-fly.');