import { useRef, useMemo } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import { Float } from "@react-three/drei";
import * as THREE from "three";

const NODE_COUNT = 32;
const GRID_SPACING = 1.1;
const EDGE_MAX_DIST = 1.6;
const PACKET_COUNT = 8;

interface GridNode {
  position: THREE.Vector3;
  phase: number;
  baseScale: number;
}

interface Edge {
  from: number;
  to: number;
}

interface Packet {
  edgeIndex: number;
  progress: number;
  speed: number;
  direction: 1 | -1;
}

/* Generate nodes on a gently curved grid */
function generateGridNodes(): GridNode[] {
  const nodes: GridNode[] = [];
  const cols = 6;
  const rows = 5;

  for (let row = 0; row < rows; row++) {
    // Offset every other row for a hex-like feel
    const colsInRow = row % 2 === 0 ? cols : cols - 1;
    const offsetX = row % 2 === 0 ? 0 : GRID_SPACING * 0.5;

    for (let col = 0; col < colsInRow; col++) {
      if (nodes.length >= NODE_COUNT) break;

      const x =
        (col - (colsInRow - 1) / 2) * GRID_SPACING + offsetX;
      const z = (row - (rows - 1) / 2) * (GRID_SPACING * 0.85);

      // Gentle curve — bowl shape
      const dist = Math.sqrt(x * x + z * z);
      const y = dist * dist * 0.03;

      // Slight random jitter
      const jx = (Math.random() - 0.5) * 0.15;
      const jz = (Math.random() - 0.5) * 0.15;

      nodes.push({
        position: new THREE.Vector3(x + jx, y, z + jz),
        phase: Math.random() * Math.PI * 2,
        baseScale: 0.03 + Math.random() * 0.015,
      });
    }
  }

  return nodes;
}

/* Generate edges between nearby nodes */
function generateEdges(nodes: GridNode[]): Edge[] {
  const edges: Edge[] = [];
  for (let i = 0; i < nodes.length; i++) {
    for (let j = i + 1; j < nodes.length; j++) {
      if (
        nodes[i].position.distanceTo(nodes[j].position) < EDGE_MAX_DIST
      ) {
        edges.push({ from: i, to: j });
      }
    }
  }
  return edges;
}

/* ─── Grid node (sphere with pulse) ─── */
function GridNodes({ nodes }: { nodes: GridNode[] }) {
  const meshRefs = useRef<(THREE.Mesh | null)[]>([]);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    meshRefs.current.forEach((mesh, i) => {
      if (!mesh) return;
      const node = nodes[i];
      const pulse = Math.sin(t * 1.5 + node.phase);
      const mat = mesh.material as THREE.MeshBasicMaterial;
      mat.opacity = 0.25 + 0.35 * Math.max(0, pulse);
      const s =
        node.baseScale * (1 + 0.4 * Math.max(0, pulse));
      mesh.scale.setScalar(s / node.baseScale);
    });
  });

  return (
    <group>
      {nodes.map((node, i) => (
        <mesh
          key={i}
          ref={(el) => {
            meshRefs.current[i] = el;
          }}
          position={node.position}
        >
          <sphereGeometry args={[node.baseScale, 10, 10]} />
          <meshBasicMaterial
            color="#d4d4d8"
            transparent
            opacity={0.3}
          />
        </mesh>
      ))}
    </group>
  );
}

/* ─── Connection lines between nodes ─── */
function ConnectionLines({
  nodes,
  edges,
}: {
  nodes: GridNode[];
  edges: Edge[];
}) {
  const lines = useMemo(() => {
    return edges.map((edge) => {
      const geo = new THREE.BufferGeometry().setFromPoints([
        nodes[edge.from].position,
        nodes[edge.to].position,
      ]);
      const mat = new THREE.LineBasicMaterial({
        color: "#a1a1aa",
        transparent: true,
        opacity: 0.06,
      });
      return new THREE.Line(geo, mat);
    });
  }, [nodes, edges]);

  return (
    <group>
      {lines.map((line, i) => (
        <primitive key={i} object={line} />
      ))}
    </group>
  );
}

/* ─── Light packets traveling along edges ─── */
function Packets({
  nodes,
  edges,
}: {
  nodes: GridNode[];
  edges: Edge[];
}) {
  const meshRefs = useRef<(THREE.Mesh | null)[]>([]);
  const glowRefs = useRef<(THREE.Mesh | null)[]>([]);
  const packetsRef = useRef<Packet[]>([]);

  // Initialize packets
  if (packetsRef.current.length === 0 && edges.length > 0) {
    packetsRef.current = Array.from(
      { length: PACKET_COUNT },
      () => ({
        edgeIndex: Math.floor(Math.random() * edges.length),
        progress: Math.random(),
        speed: 0.003 + Math.random() * 0.004,
        direction: (Math.random() > 0.5 ? 1 : -1) as 1 | -1,
      }),
    );
  }

  useFrame(() => {
    packetsRef.current.forEach((packet, i) => {
      const mesh = meshRefs.current[i];
      const glow = glowRefs.current[i];
      if (!mesh || !glow) return;

      // Advance
      packet.progress += packet.speed * packet.direction;

      // When reaching end, jump to a connected edge
      if (packet.progress > 1 || packet.progress < 0) {
        const currentEdge = edges[packet.edgeIndex];
        const endNode =
          packet.direction === 1
            ? currentEdge.to
            : currentEdge.from;

        // Find edges connected to end node
        const connected = edges
          .map((e, idx) => ({ e, idx }))
          .filter(
            ({ e, idx }) =>
              idx !== packet.edgeIndex &&
              (e.from === endNode || e.to === endNode),
          );

        if (connected.length > 0) {
          const next =
            connected[Math.floor(Math.random() * connected.length)];
          packet.edgeIndex = next.idx;
          packet.direction =
            next.e.from === endNode ? 1 : -1;
          packet.progress = packet.direction === 1 ? 0 : 1;
        } else {
          packet.direction *= -1 as 1 | -1;
          packet.progress = THREE.MathUtils.clamp(
            packet.progress,
            0,
            1,
          );
        }
      }

      // Position along edge
      const edge = edges[packet.edgeIndex];
      const from = nodes[edge.from].position;
      const to = nodes[edge.to].position;
      const pos = new THREE.Vector3().lerpVectors(
        from,
        to,
        packet.progress,
      );

      mesh.position.copy(pos);
      glow.position.copy(pos);
    });
  });

  return (
    <group>
      {Array.from({ length: PACKET_COUNT }, (_, i) => (
        <group key={i}>
          {/* Core */}
          <mesh
            ref={(el) => {
              meshRefs.current[i] = el;
            }}
          >
            <sphereGeometry args={[0.02, 8, 8]} />
            <meshBasicMaterial
              color="#ffffff"
              transparent
              opacity={0.7}
            />
          </mesh>
          {/* Glow */}
          <mesh
            ref={(el) => {
              glowRefs.current[i] = el;
            }}
          >
            <sphereGeometry args={[0.06, 8, 8]} />
            <meshBasicMaterial
              color="#d4d4d8"
              transparent
              opacity={0.08}
            />
          </mesh>
        </group>
      ))}
    </group>
  );
}

/* ─── Deploy burst — periodic flare on a random node ─── */
function DeployBursts({ nodes }: { nodes: GridNode[] }) {
  const ringRef = useRef<THREE.Mesh>(null);
  const burstNodeRef = useRef(0);
  const lastBurstRef = useRef(0);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();

    // Trigger a burst every ~5 seconds
    if (t - lastBurstRef.current > 5) {
      burstNodeRef.current = Math.floor(
        Math.random() * nodes.length,
      );
      lastBurstRef.current = t;
    }

    if (ringRef.current) {
      const elapsed = t - lastBurstRef.current;
      const node = nodes[burstNodeRef.current];
      ringRef.current.position.copy(node.position);

      if (elapsed < 1.5) {
        const scale = 1 + elapsed * 2;
        ringRef.current.scale.setScalar(scale);
        const mat = ringRef.current
          .material as THREE.MeshBasicMaterial;
        mat.opacity = 0.12 * (1 - elapsed / 1.5);
      } else {
        const mat = ringRef.current
          .material as THREE.MeshBasicMaterial;
        mat.opacity = 0;
      }
    }
  });

  return (
    <mesh ref={ringRef} rotation={[-Math.PI / 2, 0, 0]}>
      <ringGeometry args={[0.08, 0.1, 24]} />
      <meshBasicMaterial
        color="#d4d4d8"
        transparent
        opacity={0}
        side={THREE.DoubleSide}
      />
    </mesh>
  );
}

/* ─── Full scene ─── */
function ComputeMesh() {
  const groupRef = useRef<THREE.Group>(null);

  const nodes = useMemo(() => generateGridNodes(), []);
  const edges = useMemo(() => generateEdges(nodes), [nodes]);

  // Static orientation — no spinning

  return (
    <Float speed={0.5} rotationIntensity={0.01} floatIntensity={0.04}>
      <group ref={groupRef}>
        <GridNodes nodes={nodes} />
        <ConnectionLines nodes={nodes} edges={edges} />
        <Packets nodes={nodes} edges={edges} />
        <DeployBursts nodes={nodes} />
      </group>
    </Float>
  );
}

export function ComputeMeshScene() {
  return (
    <div className="w-full h-[350px] md:h-[550px] -mt-[250px] md:-mt-[400px]">
      <Canvas
        camera={{ position: [0, 3, 3.5], fov: 38 }}
        dpr={[1, 2]}
        gl={{
          antialias: true,
          alpha: true,
          toneMapping: THREE.ACESFilmicToneMapping,
          toneMappingExposure: 1,
        }}
        style={{ background: "transparent" }}
      >
        <ambientLight intensity={0.1} />
        <ComputeMesh />
      </Canvas>
    </div>
  );
}
