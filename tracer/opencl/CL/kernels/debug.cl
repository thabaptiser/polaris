#ifndef DEBUG_KERNELS_CL
#define DEBUG_KERNELS_CL

#define DEBUG_TONEMAP_EXPOSURE 1.0f

float3 debugToneMapAndGammaCorrect(float3 sample);

float3 debugToneMapAndGammaCorrect(float3 sample){
	sample *= DEBUG_TONEMAP_EXPOSURE;

	float3 mapped = sample / (sample + 1.0f);
	return clamp(pow(mapped, 1.0f / 2.2f), 0.0f, 1.0f) * 255.0f;
}

// Clear debug buffer
__kernel void debugClearBuffer(
		__global uchar4 *output
		){
	output[get_global_id(0)] = (uchar4)(0,0,0,255);
}

// Generate a depth map for primary ray intersections
__kernel void debugRayIntersectionDepth(
		__global const int *numRays,
		__global Path *paths,
		__global uint *hitFlags,
		__global Intersection *intersections,
		const float maxDepth,
		__global uchar4 *output
		){

	int globalId = get_global_id(0);
	if(globalId >= *numRays){
		return;
	}

	uint pixelIndex = paths[globalId].pixelIndex;
	float hitDist = intersections[globalId].wuvt.w;

	// No hit
	if(!hitFlags[globalId] || hitDist == FLT_MAX) {
		output[pixelIndex] = (uchar4)(0, 0, 0, 255);
		return;
	}

	uchar sd = uchar(255.0f * (1.0f - hitDist / (maxDepth + 1.0f)));
	output[pixelIndex] = (uchar4)(sd, sd, sd, 255);
}

// Render surface normals for primary ray hits.
__kernel void debugRayIntersectionNormals(
		__global Ray *rays,
		__global const int *numRays,
		__global Path *paths,
		__global uint *hitFlags,
		__global Intersection *intersections,
		__global float4 *vertices,
		__global float4 *normals,
		__global float2 *uv,
		__global uint *materialIndices,
		__global MaterialNode *materialNodes,
		// texture data
		__global TextureMetadata *texMeta,
		__global uchar *texData,
		// output
		__global uchar4 *output
		){

	int globalId = get_global_id(0);
	if(globalId >= *numRays){
		return;
	}

	uint pixelIndex = paths[globalId].pixelIndex;
	float hitDist = intersections[globalId].wuvt.w;

	// No hit
	if(!hitFlags[globalId] || hitDist == FLT_MAX) {
		output[pixelIndex] = (uchar4)(0, 0, 0, 255);
		return;
	}

	Surface surface;
	surfaceInit(&surface, intersections + globalId, vertices, normals, uv, materialIndices);

	float3 inRayDir = -rays[globalId].dir.xyz;

	MaterialNode materialNode;
	uint2 rndState = (uint2)(globalId, globalId);
	float3 bxdfTint;
	matSelectNode(paths + globalId, &surface, inRayDir, &materialNode, &bxdfTint, materialNodes, &rndState, texMeta, texData);

	// convert normal from [-1, 1] -> [0, 255]
	float3 val = (surface.normal + 1.0f) * 255.0f * 0.5f;
	output[pixelIndex] = (uchar4)((uchar)val.x, (uchar)val.y, (uchar)val.z, 255);
}

// Render emissive samples with optional masking for occluded/not-occluded rays.
__kernel void debugEmissiveSamples(
		__global Ray *rays,
		__global const int *numRays,
		__global Path *paths,
		__global uint *hitFlags,
		__global float3 *emissiveSamples,
		const uint maskOccluded,
		const uint maskNotOccluded,
		__global uchar4 *output
		){

	int globalId = get_global_id(0);
	if(globalId >= *numRays){
		return;
	}

	uint pathIndex = rayGetPathIndex(rays + globalId);
	uint pixelIndex = paths[pathIndex].pixelIndex;

	// Masked output
	if((maskOccluded && hitFlags[globalId]) || (maskNotOccluded && !hitFlags[globalId])) {
		output[pixelIndex] = (uchar4)(0, 0, 0, 255);
		return;
	} 

	// gamma correct and clamp
	float3 val = debugToneMapAndGammaCorrect(emissiveSamples[globalId]);
	output[pixelIndex] = (uchar4)((uchar)val.x, (uchar)val.y, (uchar)val.z, 255);
}

// Visualize throughput
__kernel void debugThroughput(
		__global Path *paths,
		__global uchar4 *output
		){

	int globalId = get_global_id(0);
	uint pixelIndex = paths[globalId].pixelIndex;

	// gamma correct and clamp
	float3 val = debugToneMapAndGammaCorrect(paths[globalId].throughput);
	output[pixelIndex] = (uchar4)((uchar)val.x, (uchar)val.y, (uchar)val.z, 255);
}

// Render accumulator contents
__kernel void debugAccumulator(
		const float sampleWeight,
		__global Path *paths,
		__global float3 *accumulator,
		__global uchar4 *output
		){

	int globalId = get_global_id(0);
	
	// gamma correct and clamp
	float3 val = debugToneMapAndGammaCorrect(accumulator[globalId] * sampleWeight);
	output[globalId] = (uchar4)((uchar)val.x, (uchar)val.y, (uchar)val.z, 255);
}

#endif
