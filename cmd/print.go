package main

import (
	"comp4321/database"
	"fmt"
	"bufio"
	"os"
	"strconv"
)

func main() {
	viewer, _ := database.LoadViewer("index.db")
	reader := bufio.NewReader(os.Stdin)
	defer viewer.Close()

	for {
		fmt.Println("Print: 1)Words, 2)Pages, 3)AdjList")
		fmt.Print("Enter option (q to quit): ")
		opt, _ := reader.ReadString('\n')
		num, _ := strconv.Atoi(string(opt[0]))

		if opt == "q\n"{
			break
		}

		switch num {
		case 1:
			viewer.PrintAllWords()
			break
		case 2:
			viewer.PrintAllPages()
			break
		case 3:
			viewer.PrintAdjList()
			break
		default:
			fmt.Println("Invalid option")
			break
		}

	}
}