package vectordb

import "sort"

type KDNode struct {
	Point    []float32
	Text     string
	Left     *KDNode
	Right    *KDNode
	SplitDim int
}

type KNNResult struct {
	Node       *KDNode
	DistanceSq float64
}

func insertRecursive(node *KDNode, point []float32, text string, depth, dim int) *KDNode {
	if node == nil {
		return &KDNode{Point: point, Text: text, SplitDim: depth % dim}
	}
	axis := node.SplitDim
	if point[axis] < node.Point[axis] {
		node.Left = insertRecursive(node.Left, point, text, depth+1, dim)
	} else {
		node.Right = insertRecursive(node.Right, point, text, depth+1, dim)
	}
	return node
}

func knnSearch(root *KDNode, query []float32, k int, dim int) []KNNResult {
	var results []KNNResult

	var search func(node *KDNode, depth int)
	search = func(node *KDNode, depth int) {
		if node == nil {
			return
		}

		distSq := squaredDistance(query, node.Point)
		if len(results) < k {
			results = append(results, KNNResult{node, distSq})
		} else if distSq < results[0].DistanceSq {
			results[0] = KNNResult{node, distSq}
		}

		sort.Slice(results, func(i, j int) bool {
			return results[i].DistanceSq > results[j].DistanceSq
		})

		axis := node.SplitDim
		diff := float64(query[axis] - node.Point[axis])

		var near, far *KDNode
		if diff < 0 {
			near, far = node.Left, node.Right
		} else {
			near, far = node.Right, node.Left
		}

		search(near, depth+1)
		if len(results) < k || diff*diff < results[0].DistanceSq {
			search(far, depth+1)
		}
	}

	search(root, 0)

	sort.Slice(results, func(i, j int) bool {
		return results[i].DistanceSq < results[j].DistanceSq
	})
	return results
}
