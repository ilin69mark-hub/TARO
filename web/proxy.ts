import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

const PRIVATE = ["/diary", "/history", "/profile"];

export function proxy(req: NextRequest) {
  const { pathname } = req.nextUrl;
  const isReading = pathname.startsWith("/reading/");
  const isPrivate = isReading || PRIVATE.includes(pathname);
  if (!isPrivate) return NextResponse.next();
  if (!req.cookies.has("taro_jwt")) {
    return NextResponse.redirect(new URL("/spreads", req.url));
  }
  return NextResponse.next();
}

export const config = {
  matcher: ["/reading/:path*", "/diary", "/history", "/profile"],
};
