/** @type {import('next').NextConfig} */
const nextConfig = {
  // Static-export mode: emits a fully static SPA into `out/` that the Go
  // server embeds via //go:embed. No Node runtime required at request time.
  output: "export",
  images: { unoptimized: true },
  trailingSlash: true,
  // Dev only: proxy API calls to the Go backend so the SPA can call /api/*
  // without thinking about the Go server's port.
  async rewrites() {
    if (process.env.NODE_ENV !== "development") return [];
    const goPort = process.env.GO_API_PORT || "8080";
    return [{ source: "/api/:path*", destination: `http://127.0.0.1:${goPort}/api/:path*` }];
  },
};

export default nextConfig;
