import "./globals.css";
import type { Metadata } from "next";
import { DialogProvider } from "@/lib/dialog";

export const metadata: Metadata = {
  title: "Triagent",
  description: "Agentic Incident Investigation",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <DialogProvider>{children}</DialogProvider>
      </body>
    </html>
  );
}
