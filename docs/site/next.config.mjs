/** @type {import('next').NextConfig} */

// basePath is set when the site is deployed under a sub-path on
// GitHub Pages (e.g. https://sourcehawk.github.io/triagent/). Local
// `next dev` and `next build` work with no env var set; the deploy
// workflow injects DOCS_BASE_PATH=/triagent.
const basePath = process.env.DOCS_BASE_PATH || "";

const nextConfig = {
  // Static export so the site can ship to GitHub Pages without any
  // server runtime. Matches the launcher frontend's mode so the docs
  // rendering pipeline (react-markdown + mermaid) behaves identically.
  output: "export",
  images: { unoptimized: true },
  trailingSlash: true,
  basePath,
  // Expose basePath to client components so the inline section nav
  // can build links that respect the GitHub Pages sub-path.
  env: {
    NEXT_PUBLIC_BASE_PATH: basePath,
  },
};

export default nextConfig;
