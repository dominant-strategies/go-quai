package tests

import (
	"github.com/spruce-solutions/go-quai/core/types"
)

// Example test for a fork choice scenario shown in slide00 (not a real slide)
func ForkChoiceTest_Slide05() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{ // Region1
			{ // Zone1
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "final_zone11"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_1"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "z21_1", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "", "z11_8"}, "z11_9"},
				&types.BlockGenSpec{[3]string{"", "", "z11_9"}, "z11_10"},
				&types.BlockGenSpec{[3]string{"", "z11_8", "z11_10"}, "z11_11"},
			},
			{},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"", "z11_1", "gen"}, "z21_1"},
			},
			{},
			{},
		},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide00 (not a real slide)
func ForkChoiceTest_Slide06() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
			},
			{},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "final_zone21"},
				&types.BlockGenSpec{[3]string{"z11_1", "gen", "final_zone21"}, "z21_2"},
				&types.BlockGenSpec{[3]string{"", "", "z21_2"}, "z21_3"},
				&types.BlockGenSpec{[3]string{"", "", "z21_3"}, "z21_4"},
				&types.BlockGenSpec{[3]string{"", "", "z21_4"}, "z21_5"},
				&types.BlockGenSpec{[3]string{"", "", "z21_5"}, ""},
			},
			{},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z11_1", "gen", "gen"}, "final_prime"},
				&types.BlockGenSpec{[3]string{"", "", "final_prime"}, "z31_2"},
				&types.BlockGenSpec{[3]string{"", "", "z31_2"}, "z31_3"},
				&types.BlockGenSpec{[3]string{"", "", "z31_3"}, "z31_4"},
				&types.BlockGenSpec{[3]string{"", "", "z31_4"}, "final_zone31"},
			},
			{},
			{
				&types.BlockGenSpec{[3]string{"", "final_prime", "gen"}, "z33_1"},
				&types.BlockGenSpec{[3]string{"", "z33_1", "z33_1"}, "final_zone33"},
			},
		},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide08
func ForkChoiceTest_Slide08() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{ // Region1
			{
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "z11_1"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "", "z12_4"}, "z12_5"},
			},
			{},
		},
		{},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide10
func ForkChoiceTest_Slide10() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "z11_1"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "z12_3", "z12_4"}, "final_region1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_6"},
				&types.BlockGenSpec{[3]string{"", "", "z12_6"}, "z12_7"},
				&types.BlockGenSpec{[3]string{"", "", "z12_7"}, "z12_8"},
				&types.BlockGenSpec{[3]string{"", "", "z12_8"}, "z12_9"},
				&types.BlockGenSpec{[3]string{"", "", "z12_9"}, "z12_10"},
			},
			{},
		},
		{},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide11
func ForkChoiceTest_Slide11() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "z12_1", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "", "z11_8"}, "z11_9"},
				&types.BlockGenSpec{[3]string{"", "", "z11_9"}, "z11_10"},
				&types.BlockGenSpec{[3]string{"", "", "z11_10"}, "z11_11"},
				&types.BlockGenSpec{[3]string{"", "z11_3", "z11_11"}, "final_region1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_12"}, "final_zone11"},
				&types.BlockGenSpec{[3]string{"", "", "z11_11"}, "z11_12"},
				&types.BlockGenSpec{[3]string{"", "", "z11_12"}, "z11_13"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "z12_1"},
			},
			{},
		},
		{},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide13
func ForkChoiceTest_Slide13() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "z11_2", "z11_2"}, "final_region1"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "", "z12_4"}, "z12_5"},
				&types.BlockGenSpec{[3]string{"", "", "z12_2"}, "final_zone12"},
			},
			{},
		},
		{},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide15
func ForkChoiceTest_Slide15() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "z12_3", "z11_5"}, "final_region1"},
				&types.BlockGenSpec{[3]string{"", "", "final_region1"}, "final_zone11"},
			},
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z12_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "", "z12_4"}, "z12_5"},
				&types.BlockGenSpec{[3]string{"", "", "z12_5"}, "final_zone12"},
				&types.BlockGenSpec{[3]string{"z31_3", "z11_6", "final_zone12"}, ""},
			},
			{},
		},
		{
			{},
			{
				&types.BlockGenSpec{[3]string{"z12_1", "gen", "gen"}, "final_prime"},
			},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z31_1"},
				&types.BlockGenSpec{[3]string{"", "", "z31_1"}, "final_zone31"},
				&types.BlockGenSpec{[3]string{"z12_1", "final_region3", "final_zone31"}, "z31_3"},
				&types.BlockGenSpec{[3]string{"", "", "z31_3"}, "z31_4"},
				&types.BlockGenSpec{[3]string{"", "", "z31_4"}, "z31_5"},
				&types.BlockGenSpec{[3]string{"", "", "z31_5"}, "z31_6"},
				&types.BlockGenSpec{[3]string{"", "", "z31_7"}, "z31_8"},
			},
			{}, // ... Zone2 omitted
			{
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "final_region3"},
				&types.BlockGenSpec{[3]string{"", "", "final_region3"}, "z33_2"},
				&types.BlockGenSpec{[3]string{"", "", "z33_2"}, "z33_3"},
				&types.BlockGenSpec{[3]string{"", "", "z33_3"}, "z33_4"},
				&types.BlockGenSpec{[3]string{"", "", "z33_4"}, "final_zone33"},
				&types.BlockGenSpec{[3]string{"", "z31_3", "final_zone33"}, "z33_6"},
				&types.BlockGenSpec{[3]string{"", "z33_6", "z33_6"}, ""},
			},
		},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide17
func ForkChoiceTest_Slide17() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "z11_1"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "", "z12_4"}, "z12_5"},
				&types.BlockGenSpec{[3]string{"", "", "z12_5"}, "z12_6"},
				&types.BlockGenSpec{[3]string{"", "z13_1", "z12_6"}, ""},
			},
			{
				&types.BlockGenSpec{[3]string{"", "z11_1", "gen"}, "z13_1"},
			},
		},
		{},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide18
func ForkChoiceTest_Slide18() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "z11_1"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"gen", "z11_1", "z12_3"}, ""},
			},
			{},
		},
		{},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide23
func ForkChoiceTest_Slide23() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{ // Region1
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "gen", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "z11_2", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"gen", "z11_5", "z11_4"}, ""},
			},
			{},
			{},
		},
		{}, // ... Region2 omitted
		{}, // ... Region3 omitted
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide25
func ForkChoiceTest_Slide25() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "gen", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "final_zone11"},
			},
			{},
			{},
		},
		{},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide26
func ForkChoiceTest_Slide26() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{ // Region1
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "final_zone11"},
			},
			{},
			{},
		},
		{}, // ... Region2 omitted
		{}, // ... Region3 omitted
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide27
func ForkChoiceTest_Slide27() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "z11_3", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "z11_4", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "z11_5", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "z11_6", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "z11_7", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "z11_8", "z11_8"}, "z11_9"},
				&types.BlockGenSpec{[3]string{"", "z11_9", "z11_9"}, "z11_10"},
				&types.BlockGenSpec{[3]string{"", "z11_10", "z11_10"}, "z11_11"},
				&types.BlockGenSpec{[3]string{"", "z11_11", "z11_11"}, "z11_12"},
				&types.BlockGenSpec{[3]string{"", "z11_12", "z11_12"}, "z11_13"},
				&types.BlockGenSpec{[3]string{"z11_1", "z11_3", "z11_3"}, "final_prime"},
			},
			{},
			{},
		},
		{},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide29
func ForkChoiceTest_Slide29() {

	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_2"}, "z11_3"},
				////////////////////////// TD Jumping
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "", "z11_8"}, "z11_9"},
				&types.BlockGenSpec{[3]string{"", "", "z11_9"}, "z11_10"},
				&types.BlockGenSpec{[3]string{"", "", "z11_10"}, "z11_11"},
				&types.BlockGenSpec{[3]string{"", "", "z11_11"}, "z11_12"},
				&types.BlockGenSpec{[3]string{"", "", "z11_12"}, "z11_13"},
				&types.BlockGenSpec{[3]string{"", "", "z11_13"}, "z11_14"},
				&types.BlockGenSpec{[3]string{"", "", "z11_14"}, "z11_15"},
				&types.BlockGenSpec{[3]string{"", "", "z11_15"}, "z11_16"},
				&types.BlockGenSpec{[3]string{"", "", "z11_16"}, "z11_17"},
				&types.BlockGenSpec{[3]string{"", "", "z11_17"}, "z11_18"},
				&types.BlockGenSpec{[3]string{"", "", "z11_18"}, "z11_19"},
				&types.BlockGenSpec{[3]string{"", "", "z11_19"}, "z11_20"},
				&types.BlockGenSpec{[3]string{"", "", "z11_20"}, "z11_21"},
				&types.BlockGenSpec{[3]string{"", "", "z11_21"}, "z11_22"},
				&types.BlockGenSpec{[3]string{"", "", "z11_22"}, "z11_23"},
				&types.BlockGenSpec{[3]string{"", "", "z11_23"}, "z11_24"},
				&types.BlockGenSpec{[3]string{"", "", "z11_24"}, "z11_25"},
				&types.BlockGenSpec{[3]string{"", "", "z11_25"}, "z11_26"},
				&types.BlockGenSpec{[3]string{"", "", "z11_26"}, "z11_27"},
				&types.BlockGenSpec{[3]string{"", "", "z11_27"}, "z11_28"},
				&types.BlockGenSpec{[3]string{"", "", "z11_28"}, "z11_29"},
				&types.BlockGenSpec{[3]string{"", "", "z11_29"}, "z11_30"},
				&types.BlockGenSpec{[3]string{"", "", "z11_30"}, "z11_31"},
				&types.BlockGenSpec{[3]string{"", "", "z11_31"}, "z11_32"},
				&types.BlockGenSpec{[3]string{"", "", "z11_32"}, "z11_33"},
				&types.BlockGenSpec{[3]string{"", "", "z11_33"}, "z11_34"},
				&types.BlockGenSpec{[3]string{"", "", "z11_34"}, "z11_35"},
				&types.BlockGenSpec{[3]string{"", "", "z11_35"}, "z11_36"},
				&types.BlockGenSpec{[3]string{"", "", "z11_36"}, "z11_37"},
				&types.BlockGenSpec{[3]string{"", "", "z11_37"}, "z11_38"},
				&types.BlockGenSpec{[3]string{"", "", "z11_38"}, "z11_39"},
				&types.BlockGenSpec{[3]string{"", "", "z11_39"}, "z11_40"},
				&types.BlockGenSpec{[3]string{"", "", "z11_40"}, "z11_41"},
				&types.BlockGenSpec{[3]string{"", "", "z11_41"}, "z11_42"},
				&types.BlockGenSpec{[3]string{"", "", "z11_42"}, "z11_43"},
				&types.BlockGenSpec{[3]string{"", "", "z11_43"}, "z11_44"},
				&types.BlockGenSpec{[3]string{"", "", "z11_44"}, "z11_45"},
				&types.BlockGenSpec{[3]string{"", "", "z11_45"}, "z11_46"},
				&types.BlockGenSpec{[3]string{"", "", "z11_46"}, "z11_47"},
				&types.BlockGenSpec{[3]string{"", "", "z11_47"}, "z11_48"},
				&types.BlockGenSpec{[3]string{"", "", "z11_48"}, "z11_49"},
				&types.BlockGenSpec{[3]string{"", "", "z11_49"}, "z11_50"},
				&types.BlockGenSpec{[3]string{"", "", "z11_50"}, "z11_51"},
				&types.BlockGenSpec{[3]string{"", "", "z11_51"}, "z11_52"},
				&types.BlockGenSpec{[3]string{"", "", "z11_52"}, "z11_53"},
				&types.BlockGenSpec{[3]string{"", "", "z11_53"}, "z11_54"},
				&types.BlockGenSpec{[3]string{"", "", "z11_54"}, "z11_55"},
				&types.BlockGenSpec{[3]string{"", "", "z11_55"}, "z11_56"},
				&types.BlockGenSpec{[3]string{"", "", "z11_56"}, "z11_57"},
				&types.BlockGenSpec{[3]string{"", "", "z11_57"}, "z11_58"},
				&types.BlockGenSpec{[3]string{"", "", "z11_58"}, "z11_59"},
				&types.BlockGenSpec{[3]string{"", "", "z11_59"}, "z11_60"},

				&types.BlockGenSpec{[3]string{"z21_1", "z11_3", "z11_3"}, "final_prime"},
				&types.BlockGenSpec{[3]string{"", "", "final_prime"}, "final_zone11"},
			},
			{},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z11_1", "gen", "gen"}, "z21_1"},
			},
			{},
			{},
		},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide30
func ForkChoiceTest_Slide30() {

	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_2"}, "z11_3"},
				///////////////////// TD skipping
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "", "z11_8"}, "z11_9"},
				&types.BlockGenSpec{[3]string{"", "", "z11_9"}, "z11_10"},
				&types.BlockGenSpec{[3]string{"", "", "z11_10"}, "z11_11"},
				&types.BlockGenSpec{[3]string{"", "", "z11_11"}, "z11_12"},
				&types.BlockGenSpec{[3]string{"", "", "z11_12"}, "z11_13"},
				&types.BlockGenSpec{[3]string{"", "", "z11_13"}, "z11_14"},
				&types.BlockGenSpec{[3]string{"", "", "z11_14"}, "z11_15"},
				&types.BlockGenSpec{[3]string{"", "", "z11_15"}, "z11_16"},
				&types.BlockGenSpec{[3]string{"", "", "z11_16"}, "z11_17"},
				&types.BlockGenSpec{[3]string{"", "", "z11_17"}, "z11_18"},
				&types.BlockGenSpec{[3]string{"", "", "z11_18"}, "z11_19"},
				&types.BlockGenSpec{[3]string{"", "", "z11_19"}, "z11_20"},
				&types.BlockGenSpec{[3]string{"", "", "z11_20"}, "z11_21"},
				&types.BlockGenSpec{[3]string{"", "", "z11_21"}, "z11_22"},
				&types.BlockGenSpec{[3]string{"", "", "z11_22"}, "z11_23"},
				&types.BlockGenSpec{[3]string{"", "", "z11_23"}, "z11_24"},
				&types.BlockGenSpec{[3]string{"", "", "z11_24"}, "z11_25"},
				&types.BlockGenSpec{[3]string{"", "", "z11_25"}, "z11_26"},
				&types.BlockGenSpec{[3]string{"", "", "z11_26"}, "z11_27"},
				&types.BlockGenSpec{[3]string{"", "", "z11_27"}, "z11_28"},
				&types.BlockGenSpec{[3]string{"", "", "z11_28"}, "z11_29"},
				&types.BlockGenSpec{[3]string{"", "", "z11_29"}, "z11_30"},
				&types.BlockGenSpec{[3]string{"", "", "z11_30"}, "z11_31"},
				&types.BlockGenSpec{[3]string{"", "", "z11_31"}, "z11_32"},
				&types.BlockGenSpec{[3]string{"", "", "z11_32"}, "z11_33"},
				&types.BlockGenSpec{[3]string{"", "", "z11_33"}, "z11_34"},
				&types.BlockGenSpec{[3]string{"", "", "z11_34"}, "z11_35"},
				&types.BlockGenSpec{[3]string{"", "", "z11_35"}, "z11_36"},
				&types.BlockGenSpec{[3]string{"", "", "z11_36"}, "z11_37"},
				&types.BlockGenSpec{[3]string{"", "", "z11_37"}, "z11_38"},
				&types.BlockGenSpec{[3]string{"", "", "z11_38"}, "z11_39"},
				&types.BlockGenSpec{[3]string{"", "", "z11_39"}, "z11_40"},
				&types.BlockGenSpec{[3]string{"", "", "z11_40"}, "z11_41"},
				&types.BlockGenSpec{[3]string{"", "", "z11_41"}, "z11_42"},
				&types.BlockGenSpec{[3]string{"", "", "z11_42"}, "z11_43"},
				&types.BlockGenSpec{[3]string{"", "", "z11_43"}, "z11_44"},
				&types.BlockGenSpec{[3]string{"", "", "z11_44"}, "z11_45"},
				&types.BlockGenSpec{[3]string{"", "", "z11_45"}, "z11_46"},
				&types.BlockGenSpec{[3]string{"", "", "z11_46"}, "z11_47"},
				&types.BlockGenSpec{[3]string{"", "", "z11_47"}, "z11_48"},
				&types.BlockGenSpec{[3]string{"", "", "z11_48"}, "z11_49"},
				&types.BlockGenSpec{[3]string{"", "", "z11_49"}, "z11_50"},
				&types.BlockGenSpec{[3]string{"", "", "z11_50"}, "z11_51"},
				&types.BlockGenSpec{[3]string{"", "", "z11_51"}, "z11_52"},
				&types.BlockGenSpec{[3]string{"", "", "z11_52"}, "z11_53"},
				&types.BlockGenSpec{[3]string{"", "", "z11_53"}, "z11_54"},
				&types.BlockGenSpec{[3]string{"", "", "z11_54"}, "z11_55"},
				&types.BlockGenSpec{[3]string{"", "", "z11_55"}, "z11_56"},
				&types.BlockGenSpec{[3]string{"", "", "z11_56"}, "z11_57"},
				&types.BlockGenSpec{[3]string{"", "", "z11_57"}, "z11_58"},
				&types.BlockGenSpec{[3]string{"", "", "z11_58"}, "z11_59"},
				&types.BlockGenSpec{[3]string{"", "", "z11_59"}, "z11_60"},

				&types.BlockGenSpec{[3]string{"final_prime", "z11_3", "z11_3"}, ""},
			},
			{},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z11_1", "gen", "gen"}, "final_prime"},
			},
			{},
			{},
		},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide31
func ForkChoiceTest_Slide31() {

	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_2"}, "z11_3"},
				////////TD Skipping
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "", "z11_8"}, "z11_9"},
				&types.BlockGenSpec{[3]string{"", "", "z11_9"}, "z11_10"},
				&types.BlockGenSpec{[3]string{"", "", "z11_10"}, "z11_11"},
				&types.BlockGenSpec{[3]string{"", "", "z11_11"}, "z11_12"},
				&types.BlockGenSpec{[3]string{"", "", "z11_12"}, "z11_13"},
				&types.BlockGenSpec{[3]string{"", "", "z11_13"}, "z11_14"},
				&types.BlockGenSpec{[3]string{"", "", "z11_14"}, "z11_15"},
				&types.BlockGenSpec{[3]string{"", "", "z11_15"}, "z11_16"},
				&types.BlockGenSpec{[3]string{"", "", "z11_16"}, "z11_17"},
				&types.BlockGenSpec{[3]string{"", "", "z11_17"}, "z11_18"},
				&types.BlockGenSpec{[3]string{"", "", "z11_18"}, "z11_19"},
				&types.BlockGenSpec{[3]string{"", "", "z11_19"}, "z11_20"},
				&types.BlockGenSpec{[3]string{"", "", "z11_20"}, "z11_21"},
				&types.BlockGenSpec{[3]string{"", "", "z11_21"}, "z11_22"},
				&types.BlockGenSpec{[3]string{"", "", "z11_22"}, "z11_23"},
				&types.BlockGenSpec{[3]string{"", "", "z11_23"}, "z11_24"},
				&types.BlockGenSpec{[3]string{"", "", "z11_24"}, "z11_25"},
				&types.BlockGenSpec{[3]string{"", "", "z11_25"}, "z11_26"},
				&types.BlockGenSpec{[3]string{"", "", "z11_26"}, "z11_27"},
				&types.BlockGenSpec{[3]string{"", "", "z11_27"}, "z11_28"},
				&types.BlockGenSpec{[3]string{"", "", "z11_28"}, "z11_29"},
				&types.BlockGenSpec{[3]string{"", "", "z11_29"}, "z11_30"},
				&types.BlockGenSpec{[3]string{"", "", "z11_30"}, "z11_31"},
				&types.BlockGenSpec{[3]string{"", "", "z11_31"}, "z11_32"},
				&types.BlockGenSpec{[3]string{"", "", "z11_32"}, "z11_33"},
				&types.BlockGenSpec{[3]string{"", "", "z11_33"}, "z11_34"},
				&types.BlockGenSpec{[3]string{"", "", "z11_34"}, "z11_35"},
				&types.BlockGenSpec{[3]string{"", "", "z11_35"}, "z11_36"},
				&types.BlockGenSpec{[3]string{"", "", "z11_36"}, "z11_37"},
				&types.BlockGenSpec{[3]string{"", "", "z11_37"}, "z11_38"},
				&types.BlockGenSpec{[3]string{"", "", "z11_38"}, "z11_39"},
				&types.BlockGenSpec{[3]string{"", "", "z11_39"}, "z11_40"},
				&types.BlockGenSpec{[3]string{"", "", "z11_40"}, "z11_41"},
				&types.BlockGenSpec{[3]string{"", "", "z11_41"}, "z11_42"},
				&types.BlockGenSpec{[3]string{"", "", "z11_42"}, "z11_43"},
				&types.BlockGenSpec{[3]string{"", "", "z11_43"}, "z11_44"},
				&types.BlockGenSpec{[3]string{"", "", "z11_44"}, "z11_45"},
				&types.BlockGenSpec{[3]string{"", "", "z11_45"}, "z11_46"},
				&types.BlockGenSpec{[3]string{"", "", "z11_46"}, "z11_47"},
				&types.BlockGenSpec{[3]string{"", "", "z11_47"}, "z11_48"},
				&types.BlockGenSpec{[3]string{"", "", "z11_48"}, "z11_49"},
				&types.BlockGenSpec{[3]string{"", "", "z11_49"}, "z11_50"},
				&types.BlockGenSpec{[3]string{"", "", "z11_50"}, "z11_51"},
				&types.BlockGenSpec{[3]string{"", "", "z11_51"}, "z11_52"},
				&types.BlockGenSpec{[3]string{"", "", "z11_52"}, "z11_53"},
				&types.BlockGenSpec{[3]string{"", "", "z11_53"}, "z11_54"},
				&types.BlockGenSpec{[3]string{"", "", "z11_54"}, "z11_55"},
				&types.BlockGenSpec{[3]string{"", "", "z11_55"}, "z11_56"},
				&types.BlockGenSpec{[3]string{"", "", "z11_56"}, "z11_57"},
				&types.BlockGenSpec{[3]string{"", "", "z11_57"}, "z11_58"},
				&types.BlockGenSpec{[3]string{"", "", "z11_58"}, "z11_59"},
				&types.BlockGenSpec{[3]string{"", "", "z11_59"}, "z11_60"},

				&types.BlockGenSpec{[3]string{"z21_1", "z11_3", "z11_3"}, "z11_61"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "final_zone11"},
			},
			{
				//&types.BlockGenSpec{[3]string{"z21_1", "z11_3", ""}, ""},
			},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z11_1", "gen", "gen"}, "z21_1"},
			},
			{},
			{},
		},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide32
func ForkChoiceTest_Slide32() {

	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{ // Region1
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_2"}, "z11_3"},
				////////////////// TD Skipping
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "", "z11_8"}, "z11_9"},
				&types.BlockGenSpec{[3]string{"", "", "z11_9"}, "z11_10"},
				&types.BlockGenSpec{[3]string{"", "", "z11_10"}, "z11_11"},
				&types.BlockGenSpec{[3]string{"", "", "z11_11"}, "z11_12"},
				&types.BlockGenSpec{[3]string{"", "", "z11_12"}, "z11_13"},
				&types.BlockGenSpec{[3]string{"", "", "z11_13"}, "z11_14"},
				&types.BlockGenSpec{[3]string{"", "", "z11_14"}, "z11_15"},
				&types.BlockGenSpec{[3]string{"", "", "z11_15"}, "z11_16"},
				&types.BlockGenSpec{[3]string{"", "", "z11_16"}, "z11_17"},
				&types.BlockGenSpec{[3]string{"", "", "z11_17"}, "z11_18"},
				&types.BlockGenSpec{[3]string{"", "", "z11_18"}, "z11_19"},
				&types.BlockGenSpec{[3]string{"", "", "z11_19"}, "z11_20"},
				&types.BlockGenSpec{[3]string{"", "", "z11_20"}, "z11_21"},
				&types.BlockGenSpec{[3]string{"", "", "z11_21"}, "z11_22"},
				&types.BlockGenSpec{[3]string{"", "", "z11_22"}, "z11_23"},
				&types.BlockGenSpec{[3]string{"", "", "z11_23"}, "z11_24"},
				&types.BlockGenSpec{[3]string{"", "", "z11_24"}, "z11_25"},
				&types.BlockGenSpec{[3]string{"", "", "z11_25"}, "z11_26"},
				&types.BlockGenSpec{[3]string{"", "", "z11_26"}, "z11_27"},
				&types.BlockGenSpec{[3]string{"", "", "z11_27"}, "z11_28"},
				&types.BlockGenSpec{[3]string{"", "", "z11_28"}, "z11_29"},
				&types.BlockGenSpec{[3]string{"", "", "z11_29"}, "z11_30"},
				&types.BlockGenSpec{[3]string{"", "", "z11_30"}, "z11_31"},
				&types.BlockGenSpec{[3]string{"", "", "z11_31"}, "z11_32"},
				&types.BlockGenSpec{[3]string{"", "", "z11_32"}, "z11_33"},
				&types.BlockGenSpec{[3]string{"", "", "z11_33"}, "z11_34"},
				&types.BlockGenSpec{[3]string{"", "", "z11_34"}, "z11_35"},
				&types.BlockGenSpec{[3]string{"", "", "z11_35"}, "z11_36"},
				&types.BlockGenSpec{[3]string{"", "", "z11_36"}, "z11_37"},
				&types.BlockGenSpec{[3]string{"", "", "z11_37"}, "z11_38"},
				&types.BlockGenSpec{[3]string{"", "", "z11_38"}, "z11_39"},
				&types.BlockGenSpec{[3]string{"", "", "z11_39"}, "z11_40"},
				&types.BlockGenSpec{[3]string{"", "", "z11_40"}, "z11_41"},
				&types.BlockGenSpec{[3]string{"", "", "z11_41"}, "z11_42"},
				&types.BlockGenSpec{[3]string{"", "", "z11_42"}, "z11_43"},
				&types.BlockGenSpec{[3]string{"", "", "z11_43"}, "z11_44"},
				&types.BlockGenSpec{[3]string{"", "", "z11_44"}, "z11_45"},
				&types.BlockGenSpec{[3]string{"", "", "z11_45"}, "z11_46"},
				&types.BlockGenSpec{[3]string{"", "", "z11_46"}, "z11_47"},
				&types.BlockGenSpec{[3]string{"", "", "z11_47"}, "z11_48"},
				&types.BlockGenSpec{[3]string{"", "", "z11_48"}, "z11_49"},
				&types.BlockGenSpec{[3]string{"", "", "z11_49"}, "z11_50"},
				&types.BlockGenSpec{[3]string{"", "", "z11_50"}, "z11_51"},
				&types.BlockGenSpec{[3]string{"", "", "z11_51"}, "z11_52"},
				&types.BlockGenSpec{[3]string{"", "", "z11_52"}, "z11_53"},
				&types.BlockGenSpec{[3]string{"", "", "z11_53"}, "z11_54"},
				&types.BlockGenSpec{[3]string{"", "", "z11_54"}, "z11_55"},
				&types.BlockGenSpec{[3]string{"", "", "z11_55"}, "z11_56"},
				&types.BlockGenSpec{[3]string{"", "", "z11_56"}, "z11_57"},
				&types.BlockGenSpec{[3]string{"", "", "z11_57"}, "z11_58"},
				&types.BlockGenSpec{[3]string{"", "", "z11_58"}, "z11_59"},
				&types.BlockGenSpec{[3]string{"", "", "z11_59"}, "z11_60"},

				&types.BlockGenSpec{[3]string{"z21_1", "z11_3", "z11_3"}, "z11_61"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "final_zone11"},
			},
			{
				//&types.BlockGenSpec{[3]string{"z31_1", "z11_3", ""}, ""},
			},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z11_1", "gen", "gen"}, "z21_1"},
			},
			{},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z21_1", "gen", "gen"}, "z31_1"},
			},
			{},
			{},
		},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide36
func ForkChoiceTest_Slide36() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"z31_1", "z13_1", "z11_6"}, "final_zone11"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "z12_3", "z12_4"}, "z12_5"},
				&types.BlockGenSpec{[3]string{"", "", "z12_5"}, "z12_6"},
				&types.BlockGenSpec{[3]string{"", "", "z12_6"}, "z12_7"},
				&types.BlockGenSpec{[3]string{"", "", "z12_7"}, "z12_8"},
				&types.BlockGenSpec{[3]string{"", "", "z12_8"}, "z12_9"},
				&types.BlockGenSpec{[3]string{"", "", "z12_9"}, "z12_10"},
				&types.BlockGenSpec{[3]string{"", "z13_2", "z12_10"}, "z12_11"},
				&types.BlockGenSpec{[3]string{"", "", "z12_11"}, "z12_12"},
				&types.BlockGenSpec{[3]string{"final_zone11", "z12_11", "z12_12"}, "final_prime"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "z12_5", "gen"}, "z13_1"},
				&types.BlockGenSpec{[3]string{"", "final_zone11", "z13_1"}, "z13_2"},
			},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z12_3", "gen", "gen"}, "z21_1"},
			},
			{},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z21_1", "gen", "gen"}, "z31_1"},
			},
			{},
			{},
		},
	}
	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide37
func ForkChoiceTest_Slide37() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "z13_1", "z11_6"}, "final_zone11"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "z12_3", "z12_4"}, "z12_5"},
				&types.BlockGenSpec{[3]string{"", "", "z12_5"}, "z12_6"},
				&types.BlockGenSpec{[3]string{"", "", "z12_6"}, "z12_7"},
				&types.BlockGenSpec{[3]string{"", "", "z12_7"}, "z12_8"},
				&types.BlockGenSpec{[3]string{"", "", "z12_8"}, "z12_9"},
				&types.BlockGenSpec{[3]string{"", "", "z12_9"}, "z12_10"},
				&types.BlockGenSpec{[3]string{"", "z13_2", "z12_10"}, "z12_11"},
				&types.BlockGenSpec{[3]string{"", "", "z12_11"}, "z12_12"},
				&types.BlockGenSpec{[3]string{"z31_1", "z12_11", "z12_12"}, "final_prime"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "z12_5", "gen"}, "z13_1"},
				&types.BlockGenSpec{[3]string{"", "final_zone11", "z13_1"}, "z13_2"},
			},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z12_3", "gen", "gen"}, "z21_1"},
			},
			{},
			{},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"z21_1", "gen", "gen"}, "z31_1"},
			},
			{},
			{},
		},
	}
	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide38
func ForkChoiceTest_Slide38() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "z13_1", "z11_6"}, "final_zone11"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "", "z12_4"}, "z12_5"},
				&types.BlockGenSpec{[3]string{"", "", "z12_5"}, "z12_6"},
				&types.BlockGenSpec{[3]string{"", "", "z12_6"}, "z12_7"},
				&types.BlockGenSpec{[3]string{"", "", "z12_7"}, "z12_8"},
				&types.BlockGenSpec{[3]string{"", "", "z12_8"}, "z12_9"},
				&types.BlockGenSpec{[3]string{"", "", "z12_9"}, "z12_10"},
				&types.BlockGenSpec{[3]string{"", "z13_3", "z12_10"}, "z12_11"},
				&types.BlockGenSpec{[3]string{"", "", "z12_11"}, "z12_12"},
				&types.BlockGenSpec{[3]string{"", "z12_11", "z12_12"}, "final_region1"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "z12_3", "gen"}, "z13_1"},
				&types.BlockGenSpec{[3]string{"", "z13_1", "z13_1"}, "z13_2"},
				&types.BlockGenSpec{[3]string{"", "final_zone11", "z13_2"}, "z13_3"},
			},
		},
		{},
		{},
	}
	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide39
func ForkChoiceTest_Slide39() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "z13_1", "z11_6"}, "final_zone11"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"z11_1", "z11_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "z12_3", "z12_4"}, "z12_5"},
				&types.BlockGenSpec{[3]string{"", "", "z12_5"}, "z12_6"},
				&types.BlockGenSpec{[3]string{"", "", "z12_6"}, "z12_7"},
				&types.BlockGenSpec{[3]string{"", "", "z12_7"}, "z12_8"},
				&types.BlockGenSpec{[3]string{"", "", "z12_8"}, "z12_9"},
				&types.BlockGenSpec{[3]string{"", "", "z12_9"}, "z12_10"},
				&types.BlockGenSpec{[3]string{"", "z13_2", "z12_10"}, "z12_11"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "z12_5", "gen"}, "z13_1"},
				&types.BlockGenSpec{[3]string{"", "final_zone11", "z13_1"}, "z13_2"},
			},
		},
		{},
		{},
	}
	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide40
func ForkChoiceTest_Slide40() {

	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "final_prime", "z11_8"}, "final_region1"},
				&types.BlockGenSpec{[3]string{"", "", "final_region1"}, "z11_10"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "", "z12_4"}, "z12_5"},
				&types.BlockGenSpec{[3]string{"", "", "z12_5"}, "z12_6"},
				&types.BlockGenSpec{[3]string{"final_zone13", "final_zone13", "z12_6"}, "z12_7"},
			},
			{
				&types.BlockGenSpec{[3]string{"final_region2", "z12_3", "gen"}, "final_zone13"},
			},
		},
		{
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z21_1"},
				&types.BlockGenSpec{[3]string{"", "", "z21_1"}, "z21_2"},
				&types.BlockGenSpec{[3]string{"z11_1", "final_zone220", "z21_2"}, "final_region2"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z22_1"},
				&types.BlockGenSpec{[3]string{"", "gen", "z22_1"}, "final_zone22"},
			},
			{}, //Zone3 omitted
		},
		{}, //Region3 omitted
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide41
func ForkChoiceTest_Slide41() {

	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"z11_1", "z11_1", "z11_2"}, "final_prime"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "", "z11_8"}, "z11_9"},
				&types.BlockGenSpec{[3]string{"", "", "z11_9"}, "z11_10"},
				&types.BlockGenSpec{[3]string{"", "", "z11_10"}, "z11_11"},
				&types.BlockGenSpec{[3]string{"", "", "z11_11"}, "z11_12"},
				&types.BlockGenSpec{[3]string{"", "", "z11_12"}, "z11_13"},
				&types.BlockGenSpec{[3]string{"", "", "z11_13"}, "z11_14"},
				&types.BlockGenSpec{[3]string{"", "", "z11_14"}, "z11_15"},
				&types.BlockGenSpec{[3]string{"", "", "z11_15"}, "z11_16"},
				&types.BlockGenSpec{[3]string{"", "", "z11_16"}, "z11_17"},
				&types.BlockGenSpec{[3]string{"", "", "z11_17"}, "z11_18"},
				&types.BlockGenSpec{[3]string{"", "", "z11_18"}, "z11_19"},
				&types.BlockGenSpec{[3]string{"", "", "z11_19"}, "z11_20"},
				&types.BlockGenSpec{[3]string{"", "", "z11_20"}, "z11_21"},
				&types.BlockGenSpec{[3]string{"", "", "z11_21"}, "z11_22"},
				&types.BlockGenSpec{[3]string{"", "", "z11_22"}, "z11_23"},
				&types.BlockGenSpec{[3]string{"", "", "z11_23"}, "z11_24"},
				&types.BlockGenSpec{[3]string{"", "", "z11_24"}, "z11_25"},
				&types.BlockGenSpec{[3]string{"", "", "z11_25"}, "z11_26"},
				&types.BlockGenSpec{[3]string{"", "", "z11_26"}, "z11_27"},
				&types.BlockGenSpec{[3]string{"", "", "z11_27"}, "z11_28"},
				&types.BlockGenSpec{[3]string{"", "", "z11_28"}, "z11_29"},
				&types.BlockGenSpec{[3]string{"", "", "z11_29"}, "z11_30"},
				&types.BlockGenSpec{[3]string{"", "", "z11_30"}, "z11_31"},
				&types.BlockGenSpec{[3]string{"", "", "z11_31"}, "z11_32"},
				&types.BlockGenSpec{[3]string{"", "", "z11_32"}, "z11_33"},
				&types.BlockGenSpec{[3]string{"", "", "z11_33"}, "z11_34"},
				&types.BlockGenSpec{[3]string{"", "", "z11_34"}, "z11_35"},
				&types.BlockGenSpec{[3]string{"", "", "z11_35"}, "z11_36"},
				&types.BlockGenSpec{[3]string{"", "", "z11_36"}, "z11_37"},
				&types.BlockGenSpec{[3]string{"", "", "z11_37"}, "z11_38"},
				&types.BlockGenSpec{[3]string{"", "", "z11_38"}, "z11_39"},
				&types.BlockGenSpec{[3]string{"", "", "z11_39"}, "z11_40"},
				&types.BlockGenSpec{[3]string{"", "", "z11_40"}, "z11_41"},
				&types.BlockGenSpec{[3]string{"", "", "z11_41"}, "z11_42"},
				&types.BlockGenSpec{[3]string{"", "", "z11_42"}, "z11_43"},
				&types.BlockGenSpec{[3]string{"", "", "z11_43"}, "z11_44"},
				&types.BlockGenSpec{[3]string{"", "", "z11_44"}, "z11_45"},
				&types.BlockGenSpec{[3]string{"", "", "z11_45"}, "z11_46"},
				&types.BlockGenSpec{[3]string{"", "", "z11_46"}, "z11_47"},
				&types.BlockGenSpec{[3]string{"", "", "z11_47"}, "z11_48"},
				&types.BlockGenSpec{[3]string{"", "", "z11_48"}, "z11_49"},
				&types.BlockGenSpec{[3]string{"", "", "z11_49"}, "z11_50"},
			},
			{},
			{},
		},
		{},
		{},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide42
func ForkChoiceTest_Slide42() {

	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"gen", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "z11_1", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"z11_2", "z11_2", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"z11_3", "z11_3", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_5"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "", "z11_8"}, "z11_9"},
				&types.BlockGenSpec{[3]string{"", "", "z11_9"}, "z11_10"},
				&types.BlockGenSpec{[3]string{"", "", "z11_10"}, "z11_11"},
				&types.BlockGenSpec{[3]string{"", "final_zone12", "z11_11"}, "final_region1"},
				&types.BlockGenSpec{[3]string{"", "", "final_region1"}, ""},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "", "z12_1"}, "z12_2"},
				&types.BlockGenSpec{[3]string{"", "z11_4", "z12_2"}, "z12_3"},
				&types.BlockGenSpec{[3]string{"", "", "z12_3"}, "z12_4"},
				&types.BlockGenSpec{[3]string{"", "", "z12_4"}, "z12_5"},
				&types.BlockGenSpec{[3]string{"", "", "z12_5"}, "z12_6"},
				&types.BlockGenSpec{[3]string{"final_zone13", "final_zone13", "z12_6"}, "final_prime"},
			},
			{
				&types.BlockGenSpec{[3]string{"final_region3", "z12_3", "gen"}, "final_zone13"},
			},
		},
		{},
		{
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z31_1"},
				&types.BlockGenSpec{[3]string{"", "", "z31_1"}, "z31_2"},
				&types.BlockGenSpec{[3]string{"", "final_zone32", "z31_2"}, "final_region3"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "", "gen"}, "z32_1"},
				&types.BlockGenSpec{[3]string{"", "", "z32_1"}, "final_zone32"},
			},
			{},
		},
	}

	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}

// Example test for a fork choice scenario shown in slide43
func ForkChoiceTest_Slide43() {
	/**************************************************************************
	 * Define the test scenario:
	 *************************************************************************/
	// The graph defines blocks to be mined by each of the chains in the network, to create a chain fork test scenario.
	graph := [3][3][]*types.BlockGenSpec{
		{
			{
				&types.BlockGenSpec{[3]string{"", "gen", "gen"}, "z11_1"},
				&types.BlockGenSpec{[3]string{"", "", "z11_1"}, "z11_2"},
				&types.BlockGenSpec{[3]string{"", "", "z11_2"}, "z11_3"},
				&types.BlockGenSpec{[3]string{"", "", "z11_3"}, "z11_4"},
				&types.BlockGenSpec{[3]string{"", "z12_1", "z11_4"}, "z11_5"},
				&types.BlockGenSpec{[3]string{"", "", "z11_4"}, "z11_6"},
				&types.BlockGenSpec{[3]string{"", "", "z11_6"}, "z11_7"},
				&types.BlockGenSpec{[3]string{"", "", "z11_7"}, "z11_8"},
				&types.BlockGenSpec{[3]string{"", "z12_1", "z11_8"}, "z11_9"},
			},
			{
				&types.BlockGenSpec{[3]string{"", "z11_1", "gen"}, "z12_1"},
				&types.BlockGenSpec{[3]string{"", "z11_5", "z12_1"}, "final_region1"},
			},
			{},
		},
		{},
		{},
	}
	/**************************************************************************
	 * Generate blocks and run the test scenario simulation:
	 *************************************************************************/
	clients, blocks := GenAndRun(graph)

	/**************************************************************************
	 * PASS/FAIL criteria:
	 *************************************************************************/
	dutLoc := []int{0, 0}
	clients.CheckCriteria(dutLoc, blocks)
	// TEST PASS
}
