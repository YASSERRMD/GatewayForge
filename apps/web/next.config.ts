import type { NextConfig } from "next";

const controlPlaneTarget =
  process.env.CONTROL_PLANE_INTERNAL_URL ||
  process.env.NEXT_PUBLIC_CONTROL_PLANE_URL ||
  "http://127.0.0.1:18080";

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  reactStrictMode: true,
  async rewrites() {
    return [
      {
        source: "/api/v1/:path*",
        destination: `${controlPlaneTarget.replace(/\/$/, "")}/api/v1/:path*`,
      },
    ];
  },
};

export default nextConfig;
