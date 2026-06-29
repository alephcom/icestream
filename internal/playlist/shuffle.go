package playlist

import "math/rand"

func shuffleIndices(order []int) {
	rand.Shuffle(len(order), func(i, j int) {
		order[i], order[j] = order[j], order[i]
	})
}
