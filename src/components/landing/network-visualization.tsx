import { useRef, useMemo, useState, useEffect } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import { Float } from "@react-three/drei";
import * as THREE from "three";

/* ─────────────────────────────────────────────
   Step 0/1: Nodes appear and connect
   Step 1/2: Deploy packets flow through the network
   Step 2/3: User device connects, app loads
   ───────────────────────────────────────────── */

interface NodeData {
  position: THREE.Vector3;
  baseScale: number;
  phase: number;
}

interface EdgeData {
  from: number;
  to: number;
}

/* Generate deterministic node positions on a sphere */
function generateNodes(count: number, radius: number): NodeData[] {
  const nodes: NodeData[] = [];
  const goldenRatio = (1 + Math.sqrt(5)) / 2;

  for (let i = 0; i < count; i++) {
    const theta = (2 * Math.PI * i) / goldenRatio;
    const phi = Math.acos(1 - (2 * (i + 0.5)) / count);
    const x = radius * Math.sin(phi) * Math.cos(theta);
    const y = radius * Math.sin(phi) * Math.sin(theta);
    const z = radius * Math.cos(phi);

    nodes.push({
      position: new THREE.Vector3(x, y, z),
      baseScale: 0.03 + Math.random() * 0.03,
      phase: Math.random() * Math.PI * 2,
    });
  }
  return nodes;
}

/* Generate edges between nearby nodes */
function generateEdges(nodes: NodeData[], maxDist: number): EdgeData[] {
  const edges: EdgeData[] = [];
  for (let i = 0; i < nodes.length; i++) {
    for (let j = i + 1; j < nodes.length; j++) {
      if (nodes[i].position.distanceTo(nodes[j].position) < maxDist) {
        edges.push({ from: i, to: j });
      }
    }
  }
  return edges;
}

const NODE_COUNT = 40;
const SPHERE_RADIUS = 2.8;
const EDGE_MAX_DIST = 2.2;

/* ─── Glowing Node ─── */
function GlowNode({
  node,
  active,
  highlight,
}: {
  node: NodeData;
  active: boolean;
  highlight: boolean;
}) {
  const meshRef = useRef<THREE.Mesh>(null);

  useFrame(({ clock }) => {
    if (!meshRef.current) return;
    const t = clock.getElapsedTime();
    const pulse = 1 + 0.3 * Math.sin(t * 2 + node.phase);
    const targetScale = active ? node.baseScale * pulse : 0;
    const current = meshRef.current.scale.x;
    const next = THREE.MathUtils.lerp(current, targetScale, 0.05);
    meshRef.current.scale.setScalar(next);
  });

  return (
    <mesh ref={meshRef} position={node.position} scale={0}>
      <sphereGeometry args={[1, 12, 12]} />
      <meshBasicMaterial
        color={highlight ? "#00d4aa" : "#4169E1"}
        transparent
        opacity={0.9}
      />
    </mesh>
  );
}

/* ─── Network Edges ─── */
function NetworkEdges({
  nodes,
  edges,
  active,
}: {
  nodes: NodeData[];
  edges: EdgeData[];
  active: boolean;
}) {
  const linesRef = useRef<THREE.Group>(null);
  const opacityRef = useRef(0);

  useFrame(() => {
    const target = active ? 0.15 : 0;
    opacityRef.current = THREE.MathUtils.lerp(opacityRef.current, target, 0.03);

    if (!linesRef.current) return;
    linesRef.current.children.forEach((child) => {
      const line = child as THREE.Line;
      const mat = line.material as THREE.LineBasicMaterial;
      mat.opacity = opacityRef.current;
    });
  });

  const lineObjects = useMemo(() => {
    return edges.map((edge) => {
      const geo = new THREE.BufferGeometry().setFromPoints([
        nodes[edge.from].position,
        nodes[edge.to].position,
      ]);
      const mat = new THREE.LineBasicMaterial({
        color: "#4169E1",
        transparent: true,
        opacity: 0,
      });
      return new THREE.Line(geo, mat);
    });
  }, [nodes, edges]);

  return (
    <group ref={linesRef}>
      {lineObjects.map((lineObj, i) => (
        <primitive key={i} object={lineObj} />
      ))}
    </group>
  );
}

/* ─── Deploy Packets (Step 2) ─── */
function DeployPackets({
  nodes,
  edges,
  active,
}: {
  nodes: NodeData[];
  edges: EdgeData[];
  active: boolean;
}) {
  const groupRef = useRef<THREE.Group>(null);
  const packetCount = 12;

  // Each packet travels along a random edge
  const packetData = useMemo(() => {
    return Array.from({ length: packetCount }, (_, i) => {
      const edge = edges[i % edges.length];
      return {
        from: nodes[edge.from].position,
        to: nodes[edge.to].position,
        speed: 0.3 + Math.random() * 0.4,
        offset: Math.random(),
      };
    });
  }, [nodes, edges]);

  useFrame(({ clock }) => {
    if (!groupRef.current) return;
    const t = clock.getElapsedTime();

    groupRef.current.children.forEach((child, i) => {
      const mesh = child as THREE.Mesh;
      const data = packetData[i];
      const progress = ((t * data.speed + data.offset) % 1);

      mesh.position.lerpVectors(data.from, data.to, progress);
      const targetScale = active ? 0.035 : 0;
      const current = mesh.scale.x;
      mesh.scale.setScalar(THREE.MathUtils.lerp(current, targetScale, 0.05));
    });
  });

  return (
    <group ref={groupRef}>
      {packetData.map((_, i) => (
        <mesh key={i} scale={0}>
          <sphereGeometry args={[1, 8, 8]} />
          <meshBasicMaterial color="#00d4aa" transparent opacity={0.8} />
        </mesh>
      ))}
    </group>
  );
}

/* ─── User Device (Step 3) ─── */
function UserDevice({ active }: { active: boolean }) {
  const groupRef = useRef<THREE.Group>(null);
  const beamRef = useRef<THREE.Mesh>(null);

  useFrame(({ clock }) => {
    if (!groupRef.current) return;
    const targetScale = active ? 1 : 0;
    const current = groupRef.current.scale.x;
    const next = THREE.MathUtils.lerp(current, targetScale, 0.04);
    groupRef.current.scale.setScalar(next);

    if (beamRef.current) {
      const t = clock.getElapsedTime();
      const mat = beamRef.current.material as THREE.MeshBasicMaterial;
      mat.opacity = active ? 0.1 + 0.05 * Math.sin(t * 3) : 0;
    }
  });

  return (
    <group ref={groupRef} position={[0, -3.5, 1.5]} scale={0}>
      <Float speed={2} rotationIntensity={0} floatIntensity={0.3}>
        {/* Phone shape */}
        <mesh>
          <boxGeometry args={[0.35, 0.6, 0.03]} />
          <meshBasicMaterial color="#ffffff" transparent opacity={0.15} />
        </mesh>
        {/* Screen */}
        <mesh position={[0, 0, 0.02]}>
          <planeGeometry args={[0.28, 0.48]} />
          <meshBasicMaterial color="#00d4aa" transparent opacity={0.3} />
        </mesh>
      </Float>
      {/* Connection beam to network */}
      <mesh ref={beamRef} position={[0, 1.5, 0]}>
        <cylinderGeometry args={[0.005, 0.05, 3, 8]} />
        <meshBasicMaterial color="#00d4aa" transparent opacity={0} />
      </mesh>
    </group>
  );
}

/* ─── Scene ─── */
function NetworkScene({ step }: { step: number }) {
  const groupRef = useRef<THREE.Group>(null);

  const nodes = useMemo(() => generateNodes(NODE_COUNT, SPHERE_RADIUS), []);
  const edges = useMemo(() => generateEdges(nodes, EDGE_MAX_DIST), [nodes]);

  useFrame(({ clock }) => {
    if (!groupRef.current) return;
    groupRef.current.rotation.y = clock.getElapsedTime() * 0.05;
  });

  return (
    <group ref={groupRef}>
      {/* Edges */}
      <NetworkEdges nodes={nodes} edges={edges} active={step >= 0} />

      {/* Nodes */}
      {nodes.map((node, i) => (
        <GlowNode
          key={i}
          node={node}
          active={step >= 0}
          highlight={step >= 2 && i % 5 === 0}
        />
      ))}

      {/* Deploy packets (step 2) */}
      <DeployPackets nodes={nodes} edges={edges} active={step >= 1} />

      {/* User device (step 3) */}
      <UserDevice active={step >= 2} />
    </group>
  );
}

/* ─── Exported Component ─── */
export function NetworkVisualization({ step }: { step: number }) {
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    setMounted(true);
  }, []);

  if (!mounted) {
    return (
      <div className="w-full aspect-square max-w-[500px] mx-auto bg-surface-2/30 rounded-lg" />
    );
  }

  return (
    <div className="w-full aspect-square max-w-[500px] mx-auto">
      <Canvas
        camera={{ position: [0, 0, 7], fov: 50 }}
        dpr={[1, 1.5]}
        gl={{ antialias: true, alpha: true }}
        style={{ background: "transparent" }}
      >
        <ambientLight intensity={0.5} />
        <NetworkScene step={step} />
      </Canvas>
    </div>
  );
}
