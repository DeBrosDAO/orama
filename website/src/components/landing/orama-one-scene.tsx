import { useRef, useMemo } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import { Float, Environment, Text } from "@react-three/drei";
import * as THREE from "three";

/* ─── Orama logo as 3D dots ─── */
function OramaLogoDots({ x: offsetX }: { x: number }) {
  const groupRef = useRef<THREE.Group>(null);

  // Orama icon: outer broken circle with a gap at top-right
  const dots = useMemo(() => {
    const points: [number, number][] = [];
    const r = 0.07;

    // 14 dots evenly spaced along a 330° arc (leaving a small 30° gap at top-right)
    const dotCount = 13;
    const gapSize = Math.PI / 100; // 30° gap
    const arcSize = Math.PI * 2 - gapSize; // 330° arc
    const gapStart = -gapSize / 4; // gap centered around 0° (right side)

    for (let i = 0; i < dotCount; i++) {
      const angle = gapStart + gapSize + (i / dotCount) * arcSize;
      points.push([Math.cos(angle) * r, Math.sin(angle) * r]);
    }

    return points;
  }, []);

  // No animation — static dots, no blinking


  return (
    <group ref={groupRef} position={[offsetX, 0, 0]}>
      {dots.map(([dx, dy], i) => (
        <mesh key={i} position={[dx, 0, -dy]} rotation={[-Math.PI / 2, 0, 0]} renderOrder={10}>
          <circleGeometry args={[0.008, 12]} />
          <meshBasicMaterial color="#d4d4d8" transparent opacity={0.4} depthWrite={false} depthTest={false} side={THREE.DoubleSide} />
        </mesh>
      ))}
    </group>
  );
}

/* ─── "ne" text next to the logo ─── */
function PuckLabel() {
  // Logo replaces the "O", then "ne" follows — static, no animation
  return (
    <group position={[0.55, 0.25, 0.70]}>
      {/* Orama logo dots as the "O" */}
      <OramaLogoDots x={-0.03} />

      {/* "ne" text */}
      <Text
        position={[0.06, 0, 0]}
        rotation={[-Math.PI / 2, 0, 0]}
        fontSize={0.14}
        letterSpacing={0.02}
        fontWeight="bold"
        color="#d4d4d8"
        anchorX="left"
        anchorY="middle"
        fillOpacity={0.35}
        renderOrder={10}
      >
        ne
        <meshBasicMaterial
          color="#d4d4d8"
          transparent
          opacity={0.35}
          depthWrite={false}
          depthTest={false}
        />
      </Text>
    </group>
  );
}

/* ─── Orama One Node ─── */
function OramaOneNode() {
  const groupRef = useRef<THREE.Group>(null);
  const glowRef = useRef<THREE.Mesh>(null);
  const ledRef = useRef<THREE.Mesh>(null);

  const particles = useMemo(() => {
    const count = 20;
    const positions = new Float32Array(count * 3);
    for (let i = 0; i < count; i++) {
      const angle = Math.random() * Math.PI * 2;
      const radius = 2 + Math.random() * 2;
      positions[i * 3] = Math.cos(angle) * radius;
      positions[i * 3 + 1] = (Math.random() - 0.5) * 1;
      positions[i * 3 + 2] = Math.sin(angle) * radius;
    }
    return positions;
  }, []);

  const particlesRef = useRef<THREE.Points>(null);

  const puckGeometry = useMemo(() => {
    const w = 1.8, d = 1.8, r = 0.25;
    const shape = new THREE.Shape();
    const hw = w / 2, hd = d / 2;
    shape.moveTo(-hw + r, -hd);
    shape.lineTo(hw - r, -hd);
    shape.quadraticCurveTo(hw, -hd, hw, -hd + r);
    shape.lineTo(hw, hd - r);
    shape.quadraticCurveTo(hw, hd, hw - r, hd);
    shape.lineTo(-hw + r, hd);
    shape.quadraticCurveTo(-hw, hd, -hw, hd - r);
    shape.lineTo(-hw, -hd + r);
    shape.quadraticCurveTo(-hw, -hd, -hw + r, -hd);
    const geo = new THREE.ExtrudeGeometry(shape, {
      depth: 0.14,
      bevelEnabled: true,
      bevelThickness: 0.04,
      bevelSize: 0.04,
      bevelSegments: 8,
    });
    geo.rotateX(-Math.PI / 2);
    geo.translate(0, 0.05, 0);
    return geo;
  }, []);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    if (groupRef.current) groupRef.current.rotation.y = Math.sin(t * 0.2) * 0.12;
    if (particlesRef.current) particlesRef.current.rotation.y = t * 0.02;
    if (glowRef.current) {
      const mat = glowRef.current.material as THREE.MeshBasicMaterial;
      mat.opacity = 0.04 + 0.02 * Math.sin(t * 1.5);
    }
    if (ledRef.current) {
      const mat = ledRef.current.material as THREE.MeshBasicMaterial;
      mat.opacity = 0.3 + 0.5 * Math.sin(t * 3);
    }
  });

  return (
    <>
      <points ref={particlesRef}>
        <bufferGeometry>
          <bufferAttribute attach="attributes-position" args={[particles, 3]} />
        </bufferGeometry>
        <pointsMaterial size={0.01} color="#a1a1aa" transparent opacity={0.25} sizeAttenuation />
      </points>

      <Float speed={1.2} rotationIntensity={0.04} floatIntensity={0.15}>
        <group ref={groupRef}>
          <mesh geometry={puckGeometry} renderOrder={0}>
            <meshPhysicalMaterial
              color="#0e0e10"
              metalness={0.35}
              roughness={0.55}
              clearcoat={0.8}
              clearcoatRoughness={0.12}
            />
          </mesh>

          <PuckLabel />

          {/* Power LED — front-left, slow white blink with shine */}
          <group position={[-0.6, 0.079, 0.90]}>
            <mesh ref={ledRef} renderOrder={11}>
              <circleGeometry args={[0.025, 16]} />
              <meshBasicMaterial color="#ffffff" transparent opacity={0.6} depthWrite={false} depthTest={false} side={THREE.DoubleSide} />
            </mesh>

          </group>
        </group>
      </Float>

      <mesh ref={glowRef} position={[0, -0.15, 0]} rotation={[-Math.PI / 2, 0, 0]}>
        <ringGeometry args={[0.3, 1.6, 64]} />
        <meshBasicMaterial color="#a1a1aa" transparent opacity={0.04} side={THREE.DoubleSide} />
      </mesh>
    </>
  );
}

export function OramaOneScene() {
  return (
    <div
      className="absolute left-0 right-0 bottom-0 pointer-events-none"
      style={{ height: "70%", opacity: 0.75 }}
    >
      <Canvas
        camera={{ position: [2.2, 2.2, 2.2], fov: 28 }}
        dpr={[1, 2]}
        gl={{ antialias: true, alpha: true, toneMapping: THREE.ACESFilmicToneMapping, toneMappingExposure: 1 }}
        style={{ background: "transparent" }}
      >
        <Environment preset="city" environmentIntensity={0.2} />
        <directionalLight position={[2, 5, 2]} intensity={0.8} color="#d4d4d8" />
        <directionalLight position={[-3, 1, -2]} intensity={0.3} color="#71717a" />
        <ambientLight intensity={0.15} />
        <OramaOneNode />
      </Canvas>
    </div>
  );
}

/* ─── Inline version for the Orama One section ─── */
export function OramaOneInline() {
  return (
    <div className="w-full h-[250px]">
      <Canvas
        camera={{ position: [2.5, 2, 2.5], fov: 26 }}
        dpr={[1, 2]}
        gl={{ antialias: true, alpha: true, toneMapping: THREE.ACESFilmicToneMapping, toneMappingExposure: 1 }}
        style={{ background: "transparent" }}
      >
        <Environment preset="city" environmentIntensity={0.2} />
        <directionalLight position={[2, 5, 2]} intensity={0.8} color="#d4d4d8" />
        <directionalLight position={[-3, 1, -2]} intensity={0.3} color="#71717a" />
        <ambientLight intensity={0.15} />
        <OramaOneNode />
      </Canvas>
    </div>
  );
}
