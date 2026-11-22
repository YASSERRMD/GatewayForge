import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "GatewayForge — API Gateway & Management Studio",
  description: "Self-hosted API gateway control plane and operator console.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body className="min-h-screen antialiased">{children}</body>
    </html>
  );
}
