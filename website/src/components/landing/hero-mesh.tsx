import { useEffect, useRef } from "react";

/**
 * Animated node mesh drawn behind the hero.
 *
 * Nodes drift slowly and link to their near neighbours; packets travel along
 * the links. It is a literal picture of what Orama is — independent nodes that
 * find each other and route work between themselves.
 *
 * Canvas rather than DOM/SVG: a few hundred line segments redrawn per frame is
 * one paint here versus a few hundred composited layers there. The whole thing
 * is one element and no dependencies.
 */

const NODE_COUNT_DESKTOP = 54;
const NODE_COUNT_MOBILE = 26;
const LINK_DISTANCE = 170;
const PACKET_SPEED = 0.0075; // progress per frame along a link
const PACKET_SPAWN_CHANCE = 0.022; // per frame, while under the cap
const MAX_PACKETS = 7;
const DRIFT = 0.16; // px per frame
const MAX_DPR = 2;

interface Node {
  x: number;
  y: number;
  vx: number;
  vy: number;
  /** Phase offset so nodes don't pulse in lockstep. */
  phase: number;
}

interface Packet {
  from: number;
  to: number;
  t: number;
}

export function HeroMesh() {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const reduceMotion = window.matchMedia(
      "(prefers-reduced-motion: reduce)",
    ).matches;

    let width = 0;
    let height = 0;
    let nodes: Node[] = [];
    let packets: Packet[] = [];
    let frame = 0;
    let rafId = 0;
    let running = true;

    function seed() {
      const count =
        width < 640 ? NODE_COUNT_MOBILE : NODE_COUNT_DESKTOP;
      nodes = Array.from({ length: count }, () => ({
        x: Math.random() * width,
        y: Math.random() * height,
        vx: (Math.random() - 0.5) * DRIFT,
        vy: (Math.random() - 0.5) * DRIFT,
        phase: Math.random() * Math.PI * 2,
      }));
      packets = [];
    }

    function resize() {
      const rect = canvas!.getBoundingClientRect();
      const dpr = Math.min(window.devicePixelRatio || 1, MAX_DPR);
      width = rect.width;
      height = rect.height;
      canvas!.width = Math.round(width * dpr);
      canvas!.height = Math.round(height * dpr);
      ctx!.setTransform(dpr, 0, 0, dpr, 0, 0);
      seed();
    }

    /** Links are recomputed each frame; the node count keeps this cheap. */
    function neighbours(): [number, number, number][] {
      const out: [number, number, number][] = [];
      for (let i = 0; i < nodes.length; i++) {
        for (let j = i + 1; j < nodes.length; j++) {
          const dx = nodes[i].x - nodes[j].x;
          const dy = nodes[i].y - nodes[j].y;
          const d2 = dx * dx + dy * dy;
          if (d2 < LINK_DISTANCE * LINK_DISTANCE) {
            out.push([i, j, Math.sqrt(d2)]);
          }
        }
      }
      return out;
    }

    function draw() {
      ctx!.clearRect(0, 0, width, height);
      const links = neighbours();

      // Links — opacity falls off with distance so the mesh reads as depth.
      for (const [i, j, d] of links) {
        const strength = 1 - d / LINK_DISTANCE;
        ctx!.strokeStyle = `rgba(161, 161, 170, ${strength * 0.22})`;
        ctx!.lineWidth = 1;
        ctx!.beginPath();
        ctx!.moveTo(nodes[i].x, nodes[i].y);
        ctx!.lineTo(nodes[j].x, nodes[j].y);
        ctx!.stroke();
      }

      // Packets in flight.
      for (const p of packets) {
        const a = nodes[p.from];
        const b = nodes[p.to];
        if (!a || !b) continue;
        const x = a.x + (b.x - a.x) * p.t;
        const y = a.y + (b.y - a.y) * p.t;
        // Fade in and out at the ends of the trip.
        const alpha = Math.sin(p.t * Math.PI);
        ctx!.fillStyle = `rgba(228, 228, 231, ${alpha * 0.9})`;
        ctx!.beginPath();
        ctx!.arc(x, y, 1.8, 0, Math.PI * 2);
        ctx!.fill();
      }

      // Nodes.
      for (const n of nodes) {
        const pulse = 0.5 + 0.5 * Math.sin(frame * 0.012 + n.phase);
        ctx!.fillStyle = `rgba(212, 212, 216, ${0.25 + pulse * 0.35})`;
        ctx!.beginPath();
        ctx!.arc(n.x, n.y, 1.4, 0, Math.PI * 2);
        ctx!.fill();
      }

      return links;
    }

    function step() {
      if (!running) return;
      frame++;

      for (const n of nodes) {
        n.x += n.vx;
        n.y += n.vy;
        // Wrap rather than bounce: no visible walls, mesh feels unbounded.
        if (n.x < -20) n.x = width + 20;
        if (n.x > width + 20) n.x = -20;
        if (n.y < -20) n.y = height + 20;
        if (n.y > height + 20) n.y = -20;
      }

      const links = draw() ?? [];

      for (let i = packets.length - 1; i >= 0; i--) {
        packets[i].t += PACKET_SPEED;
        if (packets[i].t >= 1) packets.splice(i, 1);
      }
      if (
        packets.length < MAX_PACKETS &&
        links.length > 0 &&
        Math.random() < PACKET_SPAWN_CHANCE
      ) {
        const [from, to] = links[Math.floor(Math.random() * links.length)];
        packets.push({ from, to, t: 0 });
      }

      rafId = requestAnimationFrame(step);
    }

    resize();

    if (reduceMotion) {
      // One static frame: the mesh still communicates, nothing moves.
      draw();
    } else {
      rafId = requestAnimationFrame(step);
    }

    const onResize = () => {
      resize();
      if (reduceMotion) draw();
    };
    window.addEventListener("resize", onResize);

    // Don't burn frames on a backgrounded tab.
    const onVisibility = () => {
      if (reduceMotion) return;
      if (document.hidden) {
        running = false;
        cancelAnimationFrame(rafId);
      } else if (!running) {
        running = true;
        rafId = requestAnimationFrame(step);
      }
    };
    document.addEventListener("visibilitychange", onVisibility);

    return () => {
      running = false;
      cancelAnimationFrame(rafId);
      window.removeEventListener("resize", onResize);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, []);

  return (
    <div className="absolute inset-0 overflow-hidden" aria-hidden="true">
      <canvas ref={canvasRef} className="w-full h-full" />
      {/* Vignette: keeps the mesh from competing with the headline, and hides
          the hard edges where nodes wrap around. */}
      <div className="absolute inset-0 bg-[radial-gradient(ellipse_60%_50%_at_50%_45%,rgba(9,9,11,0.92)_0%,rgba(9,9,11,0.55)_45%,transparent_100%)]" />
      <div className="absolute inset-x-0 bottom-0 h-32 bg-gradient-to-b from-transparent to-surface" />
    </div>
  );
}
