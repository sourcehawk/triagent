import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "triagent — docs",
  description:
    "Agentic Incident Investigation, driven from your browser. " +
    "Documentation for the triagent launcher and MCP servers.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className="h-full">
      <body className="h-full min-h-screen bg-zinc-950 text-zinc-200 antialiased">
        {children}
      </body>
    </html>
  );
}
